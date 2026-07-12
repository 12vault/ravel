package goanalyzer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
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
		key := packageStateKey(dir, parsed.Name.Name)
		state := packages[key]
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
			packages[key] = state
		}
		state.files[file.Path] = &fileState{file: file, parsed: parsed}
		if state.name == "" {
			state.name = parsed.Name.Name
		}
	}
	assignPackageIdentities(packages, moduleRoots(files))

	for _, state := range sortedPackages(packages) {
		pkgID := statePackageID(state)
		packageEvidence := ""
		if files := sortedFiles(state.files); len(files) > 0 {
			packageEvidence = sourceEvidence(files[0].file.Path, fset.Position(files[0].parsed.Name.Pos()).Line)
		}
		result.Nodes = append(result.Nodes, graph.Node{
			ID:      pkgID,
			Kind:    graph.NodePackage,
			Name:    state.name,
			Path:    state.dir,
			Package: statePackageQualifier(state),
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
								if name.Name != "_" {
									state.declaredValues[name.Name] = true
								}
							}
						}
					}
					result.Nodes = append(result.Nodes, nodes...)
					result.Edges = append(result.Edges, edges...)
				}
			}
		}
	}

	localPackages := packagesByImportPath(packages)
	semantics := checkPackageSemantics(fset, packages, localPackages)
	result.Edges = append(result.Edges, semanticEdges(fset, packages, semantics)...)

	if a.CallGraph {
		for _, state := range sortedPackages(packages) {
			for _, fs := range sortedFiles(state.files) {
				importAliases := importsByAlias(fset, fs.file.Path, fs.parsed)
				knownTypeExpressions := qualifiedTypeExpressions(fs.parsed)
				for _, decl := range fs.parsed.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					from := functionNodeID(state, fn)
					calls := collectCalls(fset, fn, semanticInfo(semantics[state]), knownTypeExpressions)
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
									"confidence": "inferred",
									"evidence":   "import:" + resolution.importPath,
									"external":   "true",
									"language":   "go",
									"rationale":  resolution.rationale,
									"resolved":   "true",
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
	qualifier       string
	importPath      string
	importable      bool
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

func packageStateKey(dir, name string) string {
	return filepath.ToSlash(filepath.Clean(dir)) + "\x00" + strings.TrimSpace(name)
}

func assignPackageIdentities(packages map[string]*packageState, roots []moduleRoot) {
	byDir := map[string][]*packageState{}
	for _, state := range packages {
		byDir[state.dir] = append(byDir[state.dir], state)
	}
	for _, states := range byDir {
		sort.Slice(states, func(i, j int) bool { return states[i].name < states[j].name })
		primaryAssigned := false
		for _, state := range states {
			externalTest := strings.HasSuffix(state.name, "_test")
			if !primaryAssigned && !externalTest {
				state.qualifier = state.dir
				state.importable = true
				primaryAssigned = true
			} else {
				state.qualifier = qualifiedPackageDir(state.dir, state.name)
			}
			state.importPath = packageImportPath(state.dir, roots)
			if state.importPath == "" {
				state.importPath = "ravel.local/" + strings.TrimPrefix(state.qualifier, "./")
			}
			if !state.importable {
				state.importPath += "#package=" + state.name
			}
		}
	}
}

func qualifiedPackageDir(dir, name string) string {
	if dir == "" || dir == "." {
		return "#package=" + name
	}
	return dir + "#package=" + name
}

func packageImportPath(dir string, roots []moduleRoot) string {
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
		return importPath
	}
	return ""
}

func packagesByImportPath(packages map[string]*packageState) map[string]*packageState {
	out := map[string]*packageState{}
	for _, state := range sortedPackages(packages) {
		if !state.importable || strings.HasPrefix(state.importPath, "ravel.local/") {
			continue
		}
		out[state.importPath] = state
	}
	return out
}

func pathWithinModule(dir, moduleDir string) bool {
	if moduleDir == "." {
		return true
	}
	return dir == moduleDir || strings.HasPrefix(dir, moduleDir+"/")
}

type packageSemantics struct {
	pkg  *types.Package
	info *types.Info
	err  error
}

type semanticsChecker struct {
	fset     *token.FileSet
	local    map[string]*packageState
	checked  map[*packageState]*packageSemantics
	checking map[*packageState]bool
	fallback types.Importer
}

type semanticsImporter struct {
	checker *semanticsChecker
}

type standardLibraryImporter struct {
	root     string
	fallback types.Importer
	packages map[string]*types.Package
	errors   map[string]error
}

func (i semanticsImporter) Import(path string) (*types.Package, error) {
	if state := i.checker.local[path]; state != nil {
		checked := i.checker.check(state)
		if checked.pkg == nil {
			if checked.err != nil {
				return nil, checked.err
			}
			return nil, fmt.Errorf("type-check package %q did not produce a package", path)
		}
		return checked.pkg, nil
	}
	return i.checker.fallback.Import(path)
}

func (i *standardLibraryImporter) Import(importPath string) (*types.Package, error) {
	if pkg := i.packages[importPath]; pkg != nil {
		return pkg, nil
	}
	if err := i.errors[importPath]; err != nil {
		return nil, err
	}
	relative := filepath.Clean(filepath.FromSlash(importPath))
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("import %q is outside the Go standard library", importPath)
	}
	directory := filepath.Join(i.root, "src", relative)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		err = fmt.Errorf("import %q is not in the Go standard library: %w", importPath, err)
		i.errors[importPath] = err
		return nil, err
	}
	pkg, err := i.fallback.Import(importPath)
	if err != nil {
		i.errors[importPath] = err
		return nil, err
	}
	i.packages[importPath] = pkg
	return pkg, nil
}

func checkPackageSemantics(fset *token.FileSet, packages map[string]*packageState, local map[string]*packageState) map[*packageState]*packageSemantics {
	standard := &standardLibraryImporter{
		root:     runtime.GOROOT(),
		fallback: importer.Default(),
		packages: map[string]*types.Package{},
		errors:   map[string]error{},
	}
	checker := &semanticsChecker{
		fset:     fset,
		local:    local,
		checked:  map[*packageState]*packageSemantics{},
		checking: map[*packageState]bool{},
		fallback: standard,
	}
	for _, state := range sortedPackages(packages) {
		checker.check(state)
	}
	return checker.checked
}

func (c *semanticsChecker) check(state *packageState) *packageSemantics {
	if checked := c.checked[state]; checked != nil {
		return checked
	}
	if c.checking[state] {
		return &packageSemantics{err: fmt.Errorf("import cycle while checking %q", state.importPath)}
	}
	c.checking[state] = true
	checked := &packageSemantics{
		info: &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		},
	}
	c.checked[state] = checked

	parsed := make([]*ast.File, 0, len(state.files))
	for _, file := range sortedFiles(state.files) {
		parsed = append(parsed, file.parsed)
	}
	config := types.Config{
		Importer: semanticsImporter{checker: c},
		Error:    func(error) {},
	}
	checked.pkg, checked.err = config.Check(state.importPath, c.fset, parsed, checked.info)
	delete(c.checking, state)
	return checked
}

func semanticInfo(checked *packageSemantics) *types.Info {
	if checked == nil {
		return nil
	}
	return checked.info
}

type semanticTarget struct {
	id       string
	relation graph.EdgeKind
}

type declaredType struct {
	state *packageState
	id    string
	name  *types.TypeName
	named *types.Named
	path  string
	line  int
}

type semanticEdgeCandidate struct {
	edge     graph.Edge
	priority int
}

type semanticEdgeCollector struct {
	edges map[string]semanticEdgeCandidate
}

func semanticEdges(fset *token.FileSet, packages map[string]*packageState, semantics map[*packageState]*packageSemantics) []graph.Edge {
	targets, declared := semanticTargets(fset, packages, semantics)
	collector := semanticEdgeCollector{edges: map[string]semanticEdgeCandidate{}}

	for _, state := range sortedPackages(packages) {
		checked := semantics[state]
		if checked == nil || checked.info == nil {
			continue
		}
		for _, file := range sortedFiles(state.files) {
			for _, declaration := range file.parsed.Decls {
				switch declaration := declaration.(type) {
				case *ast.FuncDecl:
					collectSemanticUses(fset, checked.info, targets, &collector, functionNodeID(state, declaration), file.file.Path, declaration)
				case *ast.GenDecl:
					for _, rawSpec := range declaration.Specs {
						switch spec := rawSpec.(type) {
						case *ast.TypeSpec:
							collectSemanticUses(fset, checked.info, targets, &collector, stateTypeID(state, spec.Name.Name), file.file.Path, spec)
						case *ast.ValueSpec:
							collectValueSemanticUses(fset, checked.info, targets, &collector, state, file.file.Path, spec)
							collectImplementationAssertion(fset, checked, targets, &collector, file.file.Path, spec)
						}
					}
				}
			}
		}
	}
	collectImplicitImplementations(semantics, declared, &collector)
	return collector.sorted()
}

func semanticTargets(fset *token.FileSet, packages map[string]*packageState, semantics map[*packageState]*packageSemantics) (map[types.Object]semanticTarget, []declaredType) {
	targets := map[types.Object]semanticTarget{}
	var declared []declaredType
	for _, state := range sortedPackages(packages) {
		checked := semantics[state]
		if checked == nil || checked.info == nil {
			continue
		}
		for _, file := range sortedFiles(state.files) {
			for _, declaration := range file.parsed.Decls {
				switch declaration := declaration.(type) {
				case *ast.FuncDecl:
					if object := checked.info.Defs[declaration.Name]; object != nil {
						targets[object] = semanticTarget{id: functionNodeID(state, declaration), relation: graph.EdgeReferences}
					}
				case *ast.GenDecl:
					for _, rawSpec := range declaration.Specs {
						switch spec := rawSpec.(type) {
						case *ast.TypeSpec:
							object, _ := checked.info.Defs[spec.Name].(*types.TypeName)
							if object == nil {
								continue
							}
							id := stateTypeID(state, spec.Name.Name)
							targets[object] = semanticTarget{id: id, relation: graph.EdgeUsesType}
							if spec.Assign.IsValid() {
								continue
							}
							named, _ := types.Unalias(object.Type()).(*types.Named)
							if named != nil {
								declared = append(declared, declaredType{
									state: state,
									id:    id,
									name:  object,
									named: named,
									path:  file.file.Path,
									line:  fset.Position(spec.Name.Pos()).Line,
								})
							}
						case *ast.ValueSpec:
							for _, name := range spec.Names {
								if name.Name == "_" {
									continue
								}
								if object := checked.info.Defs[name]; object != nil {
									targets[object] = semanticTarget{id: stateTypeID(state, name.Name), relation: graph.EdgeReferences}
								}
							}
						}
					}
				}
			}
		}
	}
	return targets, declared
}

func collectValueSemanticUses(fset *token.FileSet, info *types.Info, targets map[types.Object]semanticTarget, collector *semanticEdgeCollector, state *packageState, path string, spec *ast.ValueSpec) {
	for index, name := range spec.Names {
		if name.Name == "_" {
			continue
		}
		from := stateTypeID(state, name.Name)
		collectSemanticUses(fset, info, targets, collector, from, path, spec.Type)
		if len(spec.Values) == 1 {
			collectSemanticUses(fset, info, targets, collector, from, path, spec.Values[0])
		} else if index < len(spec.Values) {
			collectSemanticUses(fset, info, targets, collector, from, path, spec.Values[index])
		}
	}
}

func collectSemanticUses(fset *token.FileSet, info *types.Info, targets map[types.Object]semanticTarget, collector *semanticEdgeCollector, from, path string, node ast.Node) {
	if info == nil || node == nil || from == "" {
		return
	}
	called := calledIdentifiers(node)
	ast.Inspect(node, func(candidate ast.Node) bool {
		identifier, ok := candidate.(*ast.Ident)
		if !ok {
			return true
		}
		object := info.Uses[identifier]
		target, found := targets[object]
		if !found || target.id == "" || target.id == from {
			return true
		}
		if target.relation == graph.EdgeReferences && called[identifier] {
			if _, isFunction := object.(*types.Func); isFunction {
				return true
			}
		}
		rationale := "go/types binds this identifier to the parsed package symbol"
		if target.relation == graph.EdgeUsesType {
			rationale = "go/types binds this type expression to the parsed type declaration"
		}
		collector.add(target.relation, from, target.id, path, fset.Position(identifier.Pos()).Line, rationale, 0, nil)
		return true
	})
}

func calledIdentifiers(node ast.Node) map[*ast.Ident]bool {
	called := map[*ast.Ident]bool{}
	ast.Inspect(node, func(candidate ast.Node) bool {
		call, ok := candidate.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch function := callableExpr(call.Fun).(type) {
		case *ast.Ident:
			called[function] = true
		case *ast.SelectorExpr:
			called[function.Sel] = true
		}
		return true
	})
	return called
}

func collectImplementationAssertion(fset *token.FileSet, checked *packageSemantics, targets map[types.Object]semanticTarget, collector *semanticEdgeCollector, path string, spec *ast.ValueSpec) {
	if checked == nil || checked.err != nil || checked.info == nil || spec.Type == nil || len(spec.Values) == 0 {
		return
	}
	for _, name := range spec.Names {
		if name.Name != "_" {
			return
		}
	}
	interfaceNamed := namedType(checked.info.TypeOf(spec.Type))
	if interfaceNamed == nil {
		return
	}
	interfaceType, ok := interfaceNamed.Underlying().(*types.Interface)
	if !ok || !interfaceType.IsMethodSet() {
		return
	}
	interfaceTarget, found := targets[interfaceNamed.Obj()]
	if !found || interfaceTarget.relation != graph.EdgeUsesType {
		return
	}
	for _, value := range spec.Values {
		valueType := checked.info.TypeOf(value)
		concreteNamed := namedType(valueType)
		if concreteNamed == nil || !types.Implements(valueType, interfaceType) {
			continue
		}
		concreteTarget, found := targets[concreteNamed.Obj()]
		if !found || concreteTarget.relation != graph.EdgeUsesType || concreteTarget.id == interfaceTarget.id {
			continue
		}
		collector.add(graph.EdgeImplements, concreteTarget.id, interfaceTarget.id, path, fset.Position(spec.Pos()).Line,
			"go/types validates the explicit compile-time implementation assertion", 2,
			map[string]string{"implementation_evidence": "compile_time_assertion"})
	}
}

func collectImplicitImplementations(semantics map[*packageState]*packageSemantics, declared []declaredType, collector *semanticEdgeCollector) {
	byState := map[*packageState][]declaredType{}
	for _, declaration := range declared {
		byState[declaration.state] = append(byState[declaration.state], declaration)
	}
	for state, declarations := range byState {
		checked := semantics[state]
		if checked == nil || checked.err != nil {
			continue
		}
		for _, concrete := range declarations {
			if _, isInterface := concrete.named.Underlying().(*types.Interface); isInterface {
				continue
			}
			for _, target := range declarations {
				interfaceType, isInterface := target.named.Underlying().(*types.Interface)
				if !isInterface || !interfaceType.IsMethodSet() || interfaceType.NumMethods() == 0 {
					continue
				}
				implementation := "value"
				implements := types.Implements(concrete.named, interfaceType)
				if !implements {
					implementation = "pointer"
					implements = types.Implements(types.NewPointer(concrete.named), interfaceType)
				}
				if !implements {
					continue
				}
				collector.add(graph.EdgeImplements, concrete.id, target.id, concrete.path, concrete.line,
					"go/types confirms the parsed concrete method set implements the parsed interface", 1,
					map[string]string{
						"implementation":          implementation,
						"interface_evidence":      sourceEvidence(target.path, target.line),
						"implementation_evidence": "method_set",
					})
			}
		}
	}
}

func namedType(value types.Type) *types.Named {
	if value == nil {
		return nil
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, _ := value.(*types.Named)
	return named
}

func (c *semanticEdgeCollector) add(kind graph.EdgeKind, from, to, path string, line int, rationale string, priority int, extra map[string]string) {
	if kind == "" || from == "" || to == "" || from == to {
		return
	}
	key := string(kind) + "\x00" + from + "\x00" + to
	if existing, found := c.edges[key]; found && existing.priority >= priority {
		return
	}
	meta := extractedMeta(path, line)
	meta["resolved"] = "true"
	meta["rationale"] = rationale
	for key, value := range extra {
		if value != "" {
			meta[key] = value
		}
	}
	c.edges[key] = semanticEdgeCandidate{
		edge: graph.Edge{
			Kind: kind,
			From: from,
			To:   to,
			Meta: meta,
		},
		priority: priority,
	}
}

func (c *semanticEdgeCollector) sorted() []graph.Edge {
	edges := make([]graph.Edge, 0, len(c.edges))
	for _, candidate := range c.edges {
		edges = append(edges, candidate.edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

func functionNode(fset *token.FileSet, state *packageState, path string, fn *ast.FuncDecl) graph.Node {
	kind := graph.NodeFunction
	id := stateFunctionID(state, fn.Name.Name)
	name := fn.Name.Name
	startLine := fset.Position(fn.Pos()).Line
	if fn.Recv != nil {
		kind = graph.NodeMethod
		receiver := receiverName(fn)
		id = stateMethodID(state, receiver, fn.Name.Name)
		name = receiver + "." + fn.Name.Name
	}
	return graph.Node{
		ID:        id,
		Kind:      kind,
		Name:      name,
		Path:      path,
		Package:   statePackageQualifier(state),
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
		return stateFunctionID(state, fn.Name.Name)
	}
	return stateMethodID(state, receiverName(fn), fn.Name.Name)
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
				ID:        stateTypeID(state, s.Name.Name),
				Kind:      kind,
				Name:      s.Name.Name,
				Path:      path,
				Package:   statePackageQualifier(state),
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
				if name.Name == "_" {
					continue
				}
				startLine := fset.Position(name.Pos()).Line
				node := graph.Node{
					ID:        stateTypeID(state, name.Name),
					Kind:      graph.NodeVariable,
					Name:      name.Name,
					Path:      path,
					Package:   statePackageQualifier(state),
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

func qualifiedTypeExpressions(file *ast.File) map[string]bool {
	typesByExpression := map[string]bool{}
	if file == nil {
		return typesByExpression
	}
	ast.Inspect(file, func(candidate ast.Node) bool {
		switch node := candidate.(type) {
		case *ast.Field:
			recordQualifiedTypeExpression(node.Type, typesByExpression)
		case *ast.TypeSpec:
			recordQualifiedTypeExpression(node.Type, typesByExpression)
		case *ast.ValueSpec:
			recordQualifiedTypeExpression(node.Type, typesByExpression)
		case *ast.CompositeLit:
			recordQualifiedTypeExpression(node.Type, typesByExpression)
		case *ast.TypeAssertExpr:
			recordQualifiedTypeExpression(node.Type, typesByExpression)
		case *ast.CallExpr:
			identifier, ok := callableExpr(node.Fun).(*ast.Ident)
			if ok && (identifier.Name == "new" || identifier.Name == "make") && len(node.Args) > 0 {
				recordQualifiedTypeExpression(node.Args[0], typesByExpression)
			}
		case *ast.IndexListExpr:
			for _, index := range node.Indices {
				recordQualifiedTypeExpression(index, typesByExpression)
			}
		}
		return true
	})
	return typesByExpression
}

func recordQualifiedTypeExpression(expression ast.Expr, typesByExpression map[string]bool) {
	if expression == nil {
		return
	}
	switch expression := unparen(expression).(type) {
	case *ast.SelectorExpr:
		if name := exprString(expression); name != "" {
			typesByExpression[name] = true
		}
	case *ast.StarExpr:
		recordQualifiedTypeExpression(expression.X, typesByExpression)
	case *ast.ArrayType:
		recordQualifiedTypeExpression(expression.Elt, typesByExpression)
	case *ast.Ellipsis:
		recordQualifiedTypeExpression(expression.Elt, typesByExpression)
	case *ast.MapType:
		recordQualifiedTypeExpression(expression.Key, typesByExpression)
		recordQualifiedTypeExpression(expression.Value, typesByExpression)
	case *ast.ChanType:
		recordQualifiedTypeExpression(expression.Value, typesByExpression)
	case *ast.FuncType:
		recordQualifiedTypesInFields(expression.TypeParams, typesByExpression)
		recordQualifiedTypesInFields(expression.Params, typesByExpression)
		recordQualifiedTypesInFields(expression.Results, typesByExpression)
	case *ast.StructType:
		recordQualifiedTypesInFields(expression.Fields, typesByExpression)
	case *ast.InterfaceType:
		recordQualifiedTypesInFields(expression.Methods, typesByExpression)
	case *ast.IndexExpr:
		recordQualifiedTypeExpression(expression.X, typesByExpression)
		recordQualifiedTypeExpression(expression.Index, typesByExpression)
	case *ast.IndexListExpr:
		recordQualifiedTypeExpression(expression.X, typesByExpression)
		for _, index := range expression.Indices {
			recordQualifiedTypeExpression(index, typesByExpression)
		}
	case *ast.UnaryExpr:
		if expression.Op == token.TILDE {
			recordQualifiedTypeExpression(expression.X, typesByExpression)
		}
	case *ast.BinaryExpr:
		if expression.Op == token.OR {
			recordQualifiedTypeExpression(expression.X, typesByExpression)
			recordQualifiedTypeExpression(expression.Y, typesByExpression)
		}
	}
}

func recordQualifiedTypesInFields(fields *ast.FieldList, typesByExpression map[string]bool) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		recordQualifiedTypeExpression(field.Type, typesByExpression)
	}
}

func collectCalls(fset *token.FileSet, fn *ast.FuncDecl, info *types.Info, knownTypeExpressions map[string]bool) []callSite {
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
			ConversionSyntax: conversionSyntax(fun) || expressionIsType(info, call.Fun),
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
		if !site.ReceiverIsLocal && knownTypeExpressions[name] {
			site.ConversionSyntax = true
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
	if call.ConversionSyntax {
		return callResolution{suppressed: true}
	}
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
		if (!call.IdentifierIsLocal && !state.declaredValues[call.Name] && call.Predeclared) || state.definedTypes[call.Name] {
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

func expressionIsType(info *types.Info, expr ast.Expr) bool {
	if info == nil || expr == nil {
		return false
	}
	if value, ok := info.Types[expr]; ok && value.IsType() {
		return true
	}
	value, ok := info.Types[callableExpr(expr)]
	return ok && value.IsType()
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

func statePackageID(state *packageState) string {
	return graph.PackageID(state.qualifier)
}

func stateTypeID(state *packageState, name string) string {
	return graph.TypeID(state.qualifier, name)
}

func stateFunctionID(state *packageState, name string) string {
	return graph.FunctionID(state.qualifier, name)
}

func stateMethodID(state *packageState, receiver, name string) string {
	return graph.MethodID(state.qualifier, receiver, name)
}

func statePackageQualifier(state *packageState) string {
	if state.qualifier == state.dir {
		return packageQualifier(state.dir, state.name)
	}
	if state.dir == "." || state.dir == "" {
		return state.name
	}
	return state.qualifier
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
