package goanalyzer

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/lang"
	"github.com/12ya/reporavel/internal/scan"
)

type Analyzer struct {
	CallGraph bool
}

func New(callGraph bool) Analyzer {
	return Analyzer{CallGraph: callGraph}
}

func (a Analyzer) Language() string {
	return "go"
}

func (a Analyzer) Extensions() []string {
	return []string{".go"}
}

func (a Analyzer) Analyze(ctx context.Context, root string, files []scan.File) (*lang.AnalysisResult, error) {
	var result lang.AnalysisResult
	goFiles := filterGoFiles(files)
	fset := token.NewFileSet()
	packages := map[string]*packageState{}

	for _, file := range goFiles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		parsed, err := parser.ParseFile(fset, file.AbsPath, nil, parser.ParseComments)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, graph.Diagnostic{
				Path:    file.Path,
				Level:   "error",
				Message: err.Error(),
			})
			continue
		}
		dir := graph.ParentDir(file.Path)
		state := packages[dir]
		if state == nil {
			state = &packageState{
				dir:       dir,
				name:      parsed.Name.Name,
				files:     map[string]*fileState{},
				functions: map[string]string{},
				methods:   map[string]string{},
			}
			packages[dir] = state
		}
		state.files[file.Path] = &fileState{file: file, parsed: parsed}
		if state.name == "" {
			state.name = parsed.Name.Name
		}
	}

	for _, state := range sortedPackages(packages) {
		pkgID := graph.PackageID(state.dir)
		result.Nodes = append(result.Nodes, graph.Node{
			ID:      pkgID,
			Kind:    graph.NodePackage,
			Name:    state.name,
			Path:    state.dir,
			Package: packageQualifier(state.dir, state.name),
			Meta: map[string]string{
				"language": "go",
			},
		})

		for _, fs := range sortedFiles(state.files) {
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeContains, From: pkgID, To: graph.FileID(fs.file.Path)})
			for _, spec := range fs.parsed.Imports {
				importPath := strings.Trim(spec.Path.Value, `"`)
				importNode := graph.Node{
					ID:   graph.ImportID(importPath),
					Kind: graph.NodeImport,
					Name: importPath,
					Meta: map[string]string{
						"language": "go",
					},
				}
				if spec.Name != nil {
					importNode.Meta["alias"] = spec.Name.Name
				}
				result.Nodes = append(result.Nodes, importNode)
				result.Edges = append(result.Edges, graph.Edge{
					Kind: graph.EdgeImports,
					From: graph.FileID(fs.file.Path),
					To:   graph.ImportID(importPath),
				})
			}
			for _, decl := range fs.parsed.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					node := functionNode(fset, state, fs.file.Path, d)
					if d.Recv == nil {
						state.functions[d.Name.Name] = node.ID
					} else {
						state.methods[methodKey(receiverName(d), d.Name.Name)] = node.ID
					}
					result.Nodes = append(result.Nodes, node)
					result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(fs.file.Path), To: node.ID})
				case *ast.GenDecl:
					nodes, edges := typeAndValueNodes(fset, state, fs.file.Path, d)
					result.Nodes = append(result.Nodes, nodes...)
					result.Edges = append(result.Edges, edges...)
				}
			}
		}
	}

	if a.CallGraph {
		for _, state := range sortedPackages(packages) {
			for _, fs := range sortedFiles(state.files) {
				for _, decl := range fs.parsed.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					from := functionNodeID(state, fn)
					importAliases := importsByAlias(fs.parsed)
					calls := collectCalls(fset, fn.Body)
					for _, call := range calls {
						to, resolved := resolveCall(state, importAliases, call)
						if !resolved {
							result.Nodes = append(result.Nodes, graph.Node{
								ID:   to,
								Kind: graph.NodeFunction,
								Name: call.Name,
								Meta: map[string]string{
									"resolved": "false",
									"language": "go",
								},
							})
						}
						meta := map[string]string{
							"name":     call.Name,
							"resolved": boolString(resolved),
							"path":     fs.file.Path,
						}
						if call.Line > 0 {
							meta["line"] = intString(call.Line)
						}
						result.Edges = append(result.Edges, graph.Edge{
							Kind: graph.EdgeCalls,
							From: from,
							To:   to,
							Meta: meta,
						})
					}
				}
			}
		}
	}

	return &result, nil
}

type packageState struct {
	dir       string
	name      string
	files     map[string]*fileState
	functions map[string]string
	methods   map[string]string
}

type fileState struct {
	file   scan.File
	parsed *ast.File
}

type callSite struct {
	Name string
	Line int
}

func filterGoFiles(files []scan.File) []scan.File {
	var out []scan.File
	for _, f := range files {
		if f.Language == "go" && strings.HasSuffix(f.Path, ".go") {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func functionNode(fset *token.FileSet, state *packageState, path string, fn *ast.FuncDecl) graph.Node {
	kind := graph.NodeFunction
	id := graph.FunctionID(state.dir, fn.Name.Name)
	name := fn.Name.Name
	if fn.Recv != nil {
		kind = graph.NodeMethod
		receiver := receiverName(fn)
		id = graph.MethodID(state.dir, receiver, fn.Name.Name)
		name = receiver + "." + fn.Name.Name
	}
	return graph.Node{
		ID:        id,
		Kind:      kind,
		Name:      name,
		Path:      path,
		Package:   packageQualifier(state.dir, state.name),
		StartLine: fset.Position(fn.Pos()).Line,
		EndLine:   fset.Position(fn.End()).Line,
		Meta: map[string]string{
			"language": "go",
		},
	}
}

func functionNodeID(state *packageState, fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return graph.FunctionID(state.dir, fn.Name.Name)
	}
	return graph.MethodID(state.dir, receiverName(fn), fn.Name.Name)
}

func typeAndValueNodes(fset *token.FileSet, state *packageState, path string, decl *ast.GenDecl) ([]graph.Node, []graph.Edge) {
	var nodes []graph.Node
	var edges []graph.Edge
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			kind := graph.NodeType
			switch s.Type.(type) {
			case *ast.StructType:
				kind = graph.NodeStruct
			case *ast.InterfaceType:
				kind = graph.NodeInterface
			}
			node := graph.Node{
				ID:        graph.TypeID(state.dir, s.Name.Name),
				Kind:      kind,
				Name:      s.Name.Name,
				Path:      path,
				Package:   packageQualifier(state.dir, state.name),
				StartLine: fset.Position(s.Pos()).Line,
				EndLine:   fset.Position(s.End()).Line,
				Meta: map[string]string{
					"language": "go",
				},
			}
			nodes = append(nodes, node)
			edges = append(edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(path), To: node.ID})
		case *ast.ValueSpec:
			for _, name := range s.Names {
				node := graph.Node{
					ID:        graph.TypeID(state.dir, name.Name),
					Kind:      graph.NodeVariable,
					Name:      name.Name,
					Path:      path,
					Package:   packageQualifier(state.dir, state.name),
					StartLine: fset.Position(name.Pos()).Line,
					EndLine:   fset.Position(name.End()).Line,
					Meta: map[string]string{
						"language": "go",
						"decl":     strings.ToLower(decl.Tok.String()),
					},
				}
				nodes = append(nodes, node)
				edges = append(edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(path), To: node.ID})
			}
		}
	}
	return nodes, edges
}

func receiverName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		return "(*" + exprString(t.X) + ")"
	default:
		return exprString(t)
	}
}

func importsByAlias(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		alias := filepath.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		out[alias] = importPath
	}
	return out
}

func collectCalls(fset *token.FileSet, body *ast.BlockStmt) []callSite {
	seen := map[string]bool{}
	var calls []callSite
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call.Fun)
		if name == "" {
			return true
		}
		key := name + "@" + intString(fset.Position(call.Pos()).Line)
		if seen[key] {
			return true
		}
		seen[key] = true
		calls = append(calls, callSite{Name: name, Line: fset.Position(call.Pos()).Line})
		return true
	})
	return calls
}

func callName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		x := exprString(e.X)
		if x == "" {
			return e.Sel.Name
		}
		return x + "." + e.Sel.Name
	default:
		return exprString(expr)
	}
}

func resolveCall(state *packageState, imports map[string]string, call callSite) (string, bool) {
	if id, ok := state.functions[call.Name]; ok {
		return id, true
	}
	if strings.Contains(call.Name, ".") {
		parts := strings.Split(call.Name, ".")
		if len(parts) == 2 {
			if importPath, ok := imports[parts[0]]; ok {
				return graph.ImportID(importPath), true
			}
			for key, id := range state.methods {
				if strings.HasSuffix(key, "."+parts[1]) {
					return id, true
				}
			}
		}
	}
	return graph.UnresolvedCallID(call.Name), false
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func packageQualifier(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return filepath.ToSlash(dir)
}

func sortedPackages(packages map[string]*packageState) []*packageState {
	keys := make([]string, 0, len(packages))
	for k := range packages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*packageState, 0, len(keys))
	for _, k := range keys {
		out = append(out, packages[k])
	}
	return out
}

func sortedFiles(files map[string]*fileState) []*fileState {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*fileState, 0, len(keys))
	for _, k := range keys {
		out = append(out, files[k])
	}
	return out
}

func methodKey(receiver, name string) string {
	return receiver + "." + name
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func intString(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
