package goanalyzer

import (
	"bufio"
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
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
				dir:             dir,
				name:            parsed.Name.Name,
				files:           map[string]*fileState{},
				functions:       map[string]string{},
				functionResults: map[string]string{},
				methods:         map[string][]methodTarget{},
				definedTypes:    map[string]bool{},
				declaredValues:  map[string]bool{},
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
		packageEvidence := ""
		if files := sortedFiles(state.files); len(files) > 0 {
			packageEvidence = sourceEvidence(files[0].file.Path, fset.Position(files[0].parsed.Name.Pos()).Line)
		}
		result.Nodes = append(result.Nodes, graph.Node{
			ID:      pkgID,
			Kind:    graph.NodePackage,
			Name:    state.name,
			Path:    state.dir,
			Package: packageQualifier(state.dir, state.name),
			Meta: map[string]string{
				"confidence": "extracted",
				"evidence":   packageEvidence,
				"language":   "go",
			},
		})

		for _, fs := range sortedFiles(state.files) {
			packageLine := fset.Position(fs.parsed.Name.Pos()).Line
			result.Edges = append(result.Edges, graph.Edge{
				Kind: graph.EdgeContains,
				From: pkgID,
				To:   graph.FileID(fs.file.Path),
				Meta: extractedMeta(fs.file.Path, packageLine),
			})
			for _, spec := range fs.parsed.Imports {
				importPath := strings.Trim(spec.Path.Value, `"`)
				importLine := fset.Position(spec.Pos()).Line
				importNode := graph.Node{
					ID:   graph.ImportID(importPath),
					Kind: graph.NodeImport,
					Name: importPath,
					Meta: map[string]string{
						"confidence": "extracted",
						"evidence":   sourceEvidence(fs.file.Path, importLine),
						"language":   "go",
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
					Meta: extractedMeta(fs.file.Path, importLine),
				})
			}
			for _, decl := range fs.parsed.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					node := functionNode(fset, state, fs.file.Path, d)
					if d.Recv == nil {
						state.functions[d.Name.Name] = node.ID
						state.functionResults[d.Name.Name] = singleResultType(d.Type.Results)
					} else {
						state.methods[d.Name.Name] = append(state.methods[d.Name.Name], methodTarget{
							base: receiverBase(d.Recv.List[0].Type),
							id:   node.ID,
						})
					}
					result.Nodes = append(result.Nodes, node)
					result.Edges = append(result.Edges, graph.Edge{
						Kind: graph.EdgeDefines,
						From: graph.FileID(fs.file.Path),
						To:   node.ID,
						Meta: extractedMeta(fs.file.Path, node.StartLine),
					})
				case *ast.GenDecl:
					nodes, edges := typeAndValueNodes(fset, state, fs.file.Path, d)
					for _, spec := range d.Specs {
						switch spec := spec.(type) {
						case *ast.TypeSpec:
							state.definedTypes[spec.Name.Name] = true
						case *ast.ValueSpec:
							for _, name := range spec.Names {
								state.declaredValues[name.Name] = true
							}
						}
					}
					result.Nodes = append(result.Nodes, nodes...)
					result.Edges = append(result.Edges, edges...)
				}
			}
		}
	}

	if a.CallGraph {
		localPackages := packagesByImportPath(packages, moduleRoots(files))
		for _, state := range sortedPackages(packages) {
			for _, fs := range sortedFiles(state.files) {
				for _, decl := range fs.parsed.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					from := functionNodeID(state, fn)
					importAliases := importsByAlias(fset, fs.file.Path, fs.parsed)
					calls := collectCalls(fset, fn)
					for _, call := range calls {
						resolution := resolveCall(state, localPackages, importAliases, from, fs.file.Path, call)
						if resolution.suppressed {
							continue
						}
						if resolution.external {
							result.Nodes = append(result.Nodes, graph.Node{
								ID:      resolution.to,
								Kind:    graph.NodeFunction,
								Name:    resolution.name,
								Package: resolution.importPath,
							Meta: map[string]string{
								"confidence":      "inferred",
								"evidence":        "import:" + resolution.importPath,
								"external":        "true",
								"language":        "go",
									"rationale":       resolution.rationale,
									"resolved":        "true",
								},
							})
						} else if !resolution.resolved {
							result.Nodes = append(result.Nodes, graph.Node{
								ID:        resolution.to,
								Kind:      graph.NodeFunction,
								Name:      call.Name,
								Path:      fs.file.Path,
								StartLine: call.Line,
								EndLine:   call.Line,
								Meta: map[string]string{
									"confidence": "inferred",
									"column":     intString(call.Column),
									"evidence":   resolution.evidence,
									"language":   "go",
									"line":       intString(call.Line),
									"path":       fs.file.Path,
									"rationale":  resolution.rationale,
									"resolved":   "false",
								},
							})
						}
						meta := map[string]string{
							"confidence": "inferred",
							"evidence":   resolution.evidence,
							"name":       call.Name,
							"path":       fs.file.Path,
							"rationale":  resolution.rationale,
							"resolved":   boolString(resolution.resolved),
						}
						if call.Line > 0 {
							meta["line"] = intString(call.Line)
						}
						if call.Column > 0 {
							meta["column"] = intString(call.Column)
						}
						if resolution.external {
							meta["external"] = "true"
							meta["import_evidence"] = resolution.importEvidence
						}
						result.Edges = append(result.Edges, graph.Edge{
							Kind: graph.EdgeCalls,
							From: from,
							To:   resolution.to,
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
	dir             string
	name            string
	files           map[string]*fileState
	functions       map[string]string
	functionResults map[string]string
	methods         map[string][]methodTarget
	definedTypes    map[string]bool
	declaredValues  map[string]bool
}

type moduleRoot struct {
	dir  string
	path string
}

type methodTarget struct {
	base string
	id   string
}

type fileState struct {
	file   scan.File
	parsed *ast.File
}

type callSite struct {
	Name              string
	Selector          string
	Receiver          string
	ReceiverType      string
	ReceiverIsLocal   bool
	IdentifierIsLocal bool
	Predeclared       bool
	ConversionSyntax  bool
	Line              int
	Column            int
}

type importBinding struct {
	path     string
	evidence string
}

type callResolution struct {
	to             string
	name           string
	importPath     string
	importEvidence string
	evidence       string
	rationale      string
	resolved       bool
	external       bool
	suppressed     bool
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

func moduleRoots(files []scan.File) []moduleRoot {
	var roots []moduleRoot
	for _, file := range files {
		if filepath.Base(file.Path) != "go.mod" {
			continue
		}
		modulePath := modulePathFromFile(file.AbsPath)
		if modulePath == "" {
			continue
		}
		roots = append(roots, moduleRoot{
			dir:  graph.ParentDir(file.Path),
			path: modulePath,
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i].dir) != len(roots[j].dir) {
			return len(roots[i].dir) > len(roots[j].dir)
		}
		if roots[i].dir != roots[j].dir {
			return roots[i].dir < roots[j].dir
		}
		return roots[i].path < roots[j].path
	})
	return roots
}

func modulePathFromFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "`\"")
		}
	}
	return ""
}

func packagesByImportPath(packages map[string]*packageState, roots []moduleRoot) map[string]*packageState {
	out := map[string]*packageState{}
	for _, state := range sortedPackages(packages) {
		dir := state.dir
		for _, root := range roots {
			if !pathWithinModule(dir, root.dir) {
				continue
			}
			relative := strings.TrimPrefix(dir, root.dir)
			relative = strings.TrimPrefix(relative, "/")
			importPath := root.path
			if relative != "" && relative != "." {
				importPath += "/" + relative
			}
			if _, exists := out[importPath]; !exists {
				out[importPath] = state
			}
			break
		}
	}
	return out
}

func pathWithinModule(dir, moduleDir string) bool {
	if moduleDir == "." {
		return true
	}
	return dir == moduleDir || strings.HasPrefix(dir, moduleDir+"/")
}

func functionNode(fset *token.FileSet, state *packageState, path string, fn *ast.FuncDecl) graph.Node {
	kind := graph.NodeFunction
	id := graph.FunctionID(state.dir, fn.Name.Name)
	name := fn.Name.Name
	startLine := fset.Position(fn.Pos()).Line
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
		StartLine: startLine,
		EndLine:   fset.Position(fn.End()).Line,
		Meta: map[string]string{
			"confidence": "extracted",
			"evidence":   sourceEvidence(path, startLine),
			"language":   "go",
		},
	}
}

func functionNodeID(state *packageState, fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return graph.FunctionID(state.dir, fn.Name.Name)
	}
	return graph.MethodID(state.dir, receiverName(fn), fn.Name.Name)
}

func singleResultType(results *ast.FieldList) string {
	if results == nil || len(results.List) != 1 {
		return ""
	}
	field := results.List[0]
	// A single field can declare multiple named results: (left, right Value).
	if len(field.Names) > 1 {
		return ""
	}
	return receiverBase(field.Type)
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
			startLine := fset.Position(s.Pos()).Line
			node := graph.Node{
				ID:        graph.TypeID(state.dir, s.Name.Name),
				Kind:      kind,
				Name:      s.Name.Name,
				Path:      path,
				Package:   packageQualifier(state.dir, state.name),
				StartLine: startLine,
				EndLine:   fset.Position(s.End()).Line,
				Meta: map[string]string{
					"confidence": "extracted",
					"evidence":   sourceEvidence(path, startLine),
					"language":   "go",
				},
			}
			nodes = append(nodes, node)
			edges = append(edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(path), To: node.ID, Meta: extractedMeta(path, startLine)})
		case *ast.ValueSpec:
			for _, name := range s.Names {
				startLine := fset.Position(name.Pos()).Line
				node := graph.Node{
					ID:        graph.TypeID(state.dir, name.Name),
					Kind:      graph.NodeVariable,
					Name:      name.Name,
					Path:      path,
					Package:   packageQualifier(state.dir, state.name),
					StartLine: startLine,
					EndLine:   fset.Position(name.End()).Line,
					Meta: map[string]string{
						"confidence": "extracted",
						"decl":       strings.ToLower(decl.Tok.String()),
						"evidence":   sourceEvidence(path, startLine),
						"language":   "go",
					},
				}
				nodes = append(nodes, node)
				edges = append(edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(path), To: node.ID, Meta: extractedMeta(path, startLine)})
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

func importsByAlias(fset *token.FileSet, path string, file *ast.File) map[string]importBinding {
	out := map[string]importBinding{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		alias := filepath.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "_" || alias == "." {
			continue
		}
		out[alias] = importBinding{
			path:     importPath,
			evidence: sourceEvidence(path, fset.Position(spec.Pos()).Line),
		}
	}
	return out
}

func collectCalls(fset *token.FileSet, fn *ast.FuncDecl) []callSite {
	bindings, locals := receiverBindings(fn)
	var calls []callSite
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fun := callableExpr(call.Fun)
		name := callName(fun)
		if name == "" {
			return true
		}
		position := fset.Position(call.Pos())
		site := callSite{
			Name:             name,
			ConversionSyntax: conversionSyntax(fun),
			Line:             position.Line,
			Column:           position.Column,
		}
		switch target := fun.(type) {
		case *ast.Ident:
			site.IdentifierIsLocal = locals[target.Name]
			site.Predeclared = types.Universe.Lookup(target.Name) != nil
		case *ast.SelectorExpr:
			site.Selector = target.Sel.Name
			site.Receiver = exprString(unparen(target.X))
			site.ReceiverType = inferReceiverType(target.X, bindings)
			if ident, ok := unparen(target.X).(*ast.Ident); ok {
				site.ReceiverIsLocal = locals[ident.Name]
			}
		}
		calls = append(calls, site)
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
		return ""
	}
}

func resolveCall(state *packageState, localPackages map[string]*packageState, imports map[string]importBinding, caller, path string, call callSite) callResolution {
	evidence := sourceEvidence(path, call.Line)
	if call.Selector == "" {
		if !call.IdentifierIsLocal {
			if id, ok := state.functions[call.Name]; ok {
				return callResolution{
					to:        id,
					evidence:  evidence,
					rationale: "callee identifier matches a parsed function in the same package",
					resolved:  true,
				}
			}
		}
		if (!call.IdentifierIsLocal && !state.declaredValues[call.Name] && call.Predeclared) || state.definedTypes[call.Name] || call.ConversionSyntax {
			return callResolution{suppressed: true}
		}
		return unresolvedCall(caller, path, call, evidence, "callee identifier could not be matched to a parsed function")
	}

	receiverType := call.ReceiverType
	if receiverType == "" && !call.ReceiverIsLocal {
		if syntacticType := receiverTypeBase(call.Receiver); state.definedTypes[syntacticType] {
			receiverType = syntacticType
		}
	}
	if receiverType != "" {
		if id, ok := uniqueMethodTarget(state, receiverType, call.Selector); ok {
			return callResolution{
				to:        id,
				evidence:  evidence,
				rationale: "receiver type and selector uniquely match a parsed method in the same package",
				resolved:  true,
			}
		}
		if targetState, targetType, rationale, ok := importedReceiverTarget(state, localPackages, imports, receiverType); ok {
			if id, unique := uniqueMethodTarget(targetState, targetType, call.Selector); unique {
				return callResolution{
					to:        id,
					evidence:  evidence,
					rationale: rationale,
					resolved:  true,
				}
			}
		}
		if imported, typeName, ok := externalReceiverTarget(state, localPackages, imports, receiverType); ok {
			name := typeName + "." + call.Selector
			return callResolution{
				to:             graph.ExternalFunctionID(imported.path, name),
				name:           name,
				importPath:     imported.path,
				importEvidence: imported.evidence,
				evidence:       evidence,
				rationale:      "receiver type is qualified by a parsed external import; external method existence is not type-checked",
				resolved:       true,
				external:       true,
			}
		}
	}

	if !call.ReceiverIsLocal {
		if imported, ok := imports[call.Receiver]; ok {
			if targetState, local := localPackages[imported.path]; local {
				if id, found := targetState.functions[call.Selector]; found {
					return callResolution{
						to:        id,
						evidence:  evidence,
						rationale: "parsed import alias maps to a parsed local package function",
						resolved:  true,
					}
				}
				if targetState.definedTypes[call.Selector] {
					return callResolution{suppressed: true}
				}
				return unresolvedCall(caller, path, call, evidence, "parsed import alias maps to a local package, but the selector does not match a parsed function")
			}
			return callResolution{
				to:             graph.ExternalFunctionID(imported.path, call.Selector),
				name:           call.Selector,
				importPath:     imported.path,
				importEvidence: imported.evidence,
				evidence:       evidence,
				rationale:      "selector is qualified by a parsed import alias; external symbol existence is not type-checked",
				resolved:       true,
				external:       true,
			}
		}
	}

	rationale := "selector receiver type could not be resolved uniquely"
	if receiverType != "" {
		rationale = "known receiver type did not uniquely match a parsed method"
	}
	return unresolvedCall(caller, path, call, evidence, rationale)
}

const callResultPrefix = "call-result:"

func importedReceiverTarget(state *packageState, localPackages map[string]*packageState, imports map[string]importBinding, receiverType string) (*packageState, string, string, bool) {
	if qualifier, function, ok := parseCallResult(receiverType); ok {
		targetState := state
		if qualifier != "" {
			imported, found := imports[qualifier]
			if !found {
				return nil, "", "", false
			}
			targetState = localPackages[imported.path]
			if targetState == nil {
				return nil, "", "", false
			}
		}
		resultType := targetState.functionResults[function]
		if resultType == "" {
			return nil, "", "", false
		}
		return targetState, resultType, "parsed constructor result type and selector uniquely match a parsed local method", true
	}

	qualifier, typeName, ok := strings.Cut(receiverTypeBase(receiverType), ".")
	if !ok || qualifier == "" || typeName == "" {
		return nil, "", "", false
	}
	imported, found := imports[qualifier]
	if !found {
		return nil, "", "", false
	}
	targetState := localPackages[imported.path]
	if targetState == nil || !targetState.definedTypes[receiverTypeBase(typeName)] {
		return nil, "", "", false
	}
	return targetState, typeName, "parsed imported receiver type and selector uniquely match a parsed local method", true
}

func externalReceiverTarget(state *packageState, localPackages map[string]*packageState, imports map[string]importBinding, receiverType string) (importBinding, string, bool) {
	if qualifier, function, ok := parseCallResult(receiverType); ok {
		if qualifier != "" {
			return importBinding{}, "", false
		}
		receiverType = state.functionResults[function]
	}
	qualifier, typeName, ok := strings.Cut(receiverTypeBase(receiverType), ".")
	if !ok || qualifier == "" || typeName == "" {
		return importBinding{}, "", false
	}
	imported, found := imports[qualifier]
	if !found || localPackages[imported.path] != nil {
		return importBinding{}, "", false
	}
	return imported, receiverTypeBase(typeName), true
}

func parseCallResult(receiverType string) (string, string, bool) {
	if !strings.HasPrefix(receiverType, callResultPrefix) {
		return "", "", false
	}
	qualified := strings.TrimPrefix(receiverType, callResultPrefix)
	if qualified == "" {
		return "", "", false
	}
	qualifier, function, found := strings.Cut(qualified, ".")
	if !found {
		return "", qualified, true
	}
	if qualifier == "" || function == "" || strings.Contains(function, ".") {
		return "", "", false
	}
	return qualifier, function, true
}

func unresolvedCall(caller, path string, call callSite, evidence, rationale string) callResolution {
	return callResolution{
		to:        graph.UnresolvedCallSiteID(caller, path, call.Name, call.Line, call.Column),
		evidence:  evidence,
		rationale: rationale,
	}
}

func uniqueMethodTarget(state *packageState, receiverType, selector string) (string, bool) {
	base := receiverTypeBase(receiverType)
	if base == "" {
		return "", false
	}
	ids := map[string]bool{}
	for _, candidate := range state.methods[selector] {
		if candidate.base == base {
			ids[candidate.id] = true
		}
	}
	if len(ids) != 1 {
		return "", false
	}
	for id := range ids {
		return id, true
	}
	return "", false
}

func receiverBindings(fn *ast.FuncDecl) (map[string]string, map[string]bool) {
	bindings := map[string]string{}
	locals := map[string]bool{}
	candidates := map[string]map[string]bool{}
	uncertain := map[string]bool{}
	record := func(name, receiverType string) {
		if receiverType == "" {
			uncertain[name] = true
			delete(bindings, name)
			return
		}
		if candidates[name] == nil {
			candidates[name] = map[string]bool{}
		}
		candidates[name][receiverType] = true
		if uncertain[name] || len(candidates[name]) != 1 {
			delete(bindings, name)
			return
		}
		bindings[name] = receiverType
	}
	bindFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			base := receiverBase(field.Type)
			for _, name := range field.Names {
				locals[name.Name] = true
				record(name.Name, base)
			}
		}
	}
	bindFields(fn.Recv)
	if fn.Type != nil {
		bindFields(fn.Type.Params)
		bindFields(fn.Type.Results)
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		switch statement := n.(type) {
		case *ast.DeclStmt:
			decl, ok := statement.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				explicitType := receiverBase(value.Type)
				for i, name := range value.Names {
					locals[name.Name] = true
					inferred := explicitType
					if inferred == "" && i < len(value.Values) {
						inferred = inferReceiverType(value.Values[i], bindings)
					}
					record(name.Name, inferred)
				}
			}
		case *ast.AssignStmt:
			for i, left := range statement.Lhs {
				name, ok := unparen(left).(*ast.Ident)
				if !ok || name.Name == "_" {
					continue
				}
				if statement.Tok != token.DEFINE {
					continue
				}
				locals[name.Name] = true
				inferred := ""
				if i < len(statement.Rhs) {
					inferred = inferReceiverType(statement.Rhs[i], bindings)
				}
				record(name.Name, inferred)
			}
		case *ast.RangeStmt:
			if statement.Tok != token.DEFINE {
				return true
			}
			for _, expression := range []ast.Expr{statement.Key, statement.Value} {
				if name, ok := expression.(*ast.Ident); ok && name.Name != "_" {
					locals[name.Name] = true
					record(name.Name, "")
				}
			}
		}
		return true
	})
	return bindings, locals
}

func inferReceiverType(expr ast.Expr, bindings map[string]string) string {
	expr = unparen(expr)
	switch value := expr.(type) {
	case *ast.Ident:
		return bindings[value.Name]
	case *ast.CompositeLit:
		return receiverBase(value.Type)
	case *ast.UnaryExpr:
		if value.Op == token.AND {
			return inferReceiverType(value.X, bindings)
		}
	case *ast.StarExpr:
		return inferReceiverType(value.X, bindings)
	case *ast.TypeAssertExpr:
		return receiverBase(value.Type)
	case *ast.CallExpr:
		switch function := callableExpr(value.Fun).(type) {
		case *ast.Ident:
			if function.Name == "new" && len(value.Args) == 1 {
				return receiverBase(value.Args[0])
			}
			return callResultPrefix + function.Name
		case *ast.SelectorExpr:
			if qualifier, ok := unparen(function.X).(*ast.Ident); ok {
				return callResultPrefix + qualifier.Name + "." + function.Sel.Name
			}
		}
	}
	return ""
}

func receiverBase(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	expr = unparen(expr)
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return exprString(value)
	case *ast.StarExpr:
		return receiverBase(value.X)
	case *ast.IndexExpr:
		return receiverBase(value.X)
	case *ast.IndexListExpr:
		return receiverBase(value.X)
	default:
		return ""
	}
}

func receiverTypeBase(receiverType string) string {
	base := strings.TrimSpace(receiverType)
	for len(base) >= 2 && strings.HasPrefix(base, "(") && strings.HasSuffix(base, ")") {
		base = strings.TrimSpace(base[1 : len(base)-1])
	}
	base = strings.TrimLeft(base, "* ")
	if generic := strings.IndexByte(base, '['); generic >= 0 {
		base = strings.TrimSpace(base[:generic])
	}
	return base
}

func callableExpr(expr ast.Expr) ast.Expr {
	for {
		expr = unparen(expr)
		switch value := expr.(type) {
		case *ast.IndexExpr:
			expr = value.X
		case *ast.IndexListExpr:
			expr = value.X
		default:
			return expr
		}
	}
}

func conversionSyntax(expr ast.Expr) bool {
	expr = unparen(expr)
	switch expr.(type) {
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.StructType, *ast.StarExpr:
		return true
	default:
		return false
	}
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
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

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func extractedMeta(path string, line int) map[string]string {
	meta := map[string]string{
		"confidence": "extracted",
		"evidence":   sourceEvidence(path, line),
		"path":       path,
	}
	if line > 0 {
		meta["line"] = intString(line)
	}
	return meta
}

func sourceEvidence(path string, line int) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if line <= 0 {
		return path
	}
	return path + ":" + intString(line)
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
