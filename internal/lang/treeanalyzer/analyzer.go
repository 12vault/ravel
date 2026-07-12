// Package treeanalyzer extracts conservative, language-neutral code structure
// with the pure-Go Tree-sitter runtime. Language-specific analyzers remain the
// authority when one is registered (notably Go, Markdown, and SQL).
package treeanalyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const parseTimeoutMicros = 2_000_000

type Analyzer struct {
	language string
}

func New(language string) *Analyzer {
	return &Analyzer{language: strings.TrimSpace(language)}
}

func (a *Analyzer) Language() string { return a.language }

// Extensions are supplied by gotreesitter's grammar registry. Ravel dispatches
// already-audited files by their scanner language, so this method is metadata.
func (a *Analyzer) Extensions() []string { return nil }

// Supports reports whether at least one file can be mapped to an embedded
// grammar. Filename detection is preferred because it distinguishes TSX and
// other extension-specific grammar variants.
func Supports(language string, files []scan.File) bool {
	switch language {
	case "go", "markdown", "sql":
		return false
	}
	for _, file := range files {
		if entryForFile(language, file.Path) != nil {
			return true
		}
	}
	return grammars.DetectLanguageByName(language) != nil
}

func entryForFile(scannerLanguage, path string) *grammars.LangEntry {
	// TSX shares the scanner's TypeScript category but requires its own grammar.
	if strings.EqualFold(filepath.Ext(path), ".tsx") {
		if entry := grammars.DetectLanguage(path); entry != nil {
			return entry
		}
	}
	// Scanner classification resolves ambiguous suffixes such as Objective-C
	// versus MATLAB .m files, so prefer it over extension-only detection.
	if scannerLanguage != "" && scannerLanguage != "unknown" {
		if entry := grammars.DetectLanguageByName(scannerLanguage); entry != nil {
			return entry
		}
	}
	return grammars.DetectLanguage(path)
}

type parsedFile struct {
	file        scan.File
	language    string
	source      []byte
	definitions []definition
	references  []reference
	heritage    []gotreesitter.HeritageRef
	imports     []gotreesitter.ImportRef
}

type definition struct {
	id        string
	name      string
	qualified string
	kind      graph.NodeKind
	path      string
	language  string
	startByte uint32
	endByte   uint32
	startLine int
	endLine   int
	column    int
}

type reference struct {
	name      string
	kind      graph.EdgeKind
	path      string
	language  string
	startByte uint32
	endByte   uint32
	startLine int
	column    int
}

func (a *Analyzer) Analyze(ctx context.Context, _ string, files []scan.File) (*lang.AnalysisResult, error) {
	return a.AnalyzeWithProgress(ctx, "", files, nil)
}

func (a *Analyzer) AnalyzeWithProgress(ctx context.Context, _ string, files []scan.File, progress func(path string, completed int)) (*lang.AnalysisResult, error) {
	result := &lang.AnalysisResult{}
	parsed := make([]parsedFile, 0, len(files))
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(file.Path, i)
		}
		entry := entryForFile(a.language, file.Path)
		if entry == nil || entry.Language == nil {
			continue
		}
		pf, diagnostics, err := parseFile(ctx, file, *entry)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, pf)
	}
	if progress != nil && len(files) > 0 {
		progress(files[len(files)-1].Path, len(files))
	}

	emitDefinitions(parsed, result)
	emitReferences(parsed, result)
	emitHeritage(parsed, result)
	emitImports(parsed, result)
	gotreesitter.DrainArenaPools()
	return result, nil
}

func parseFile(ctx context.Context, file scan.File, entry grammars.LangEntry) (parsedFile, []graph.Diagnostic, error) {
	pf := parsedFile{file: file, language: entry.Name}
	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return pf, nil, err
	}
	pf.source = data
	grammar := entry.Language()
	if grammar == nil {
		return pf, []graph.Diagnostic{{Path: file.Path, Level: "warning", Message: "Tree-sitter grammar failed to load"}}, nil
	}

	parser := gotreesitter.NewParser(grammar)
	parser.SetTimeoutMicros(parseTimeoutMicros)
	var cancelled uint32
	parser.SetCancellationFlag(&cancelled)
	stop := context.AfterFunc(ctx, func() { atomic.StoreUint32(&cancelled, 1) })
	var tree *gotreesitter.Tree
	if entry.TokenSourceFactory != nil {
		factory := func(source []byte) (gotreesitter.TokenSource, error) {
			return entry.TokenSourceFactory(source, grammar), nil
		}
		tree, err = parser.ParseWithTokenSourceFactoryStrict(data, factory)
	} else {
		tree, err = parser.ParseStrict(data)
	}
	stop()
	if err != nil {
		if tree != nil {
			tree.Release()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return pf, nil, ctxErr
		}
		return pf, []graph.Diagnostic{{Path: file.Path, Level: "warning", Message: fmt.Sprintf("Tree-sitter parse stopped: %v", err)}}, nil
	}
	if tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return pf, []graph.Diagnostic{{Path: file.Path, Level: "warning", Message: "Tree-sitter returned no syntax tree"}}, nil
	}
	defer tree.Release()

	var diagnostics []graph.Diagnostic
	if tree.RootNode().HasError() {
		diagnostics = append(diagnostics, graph.Diagnostic{Path: file.Path, Level: "warning", Message: "Tree-sitter recovered from syntax errors; extracted relationships remain syntax-backed"})
	}

	query := grammars.ResolveTagsQuery(entry)
	if query != "" {
		tagger, tagErr := gotreesitter.NewTagger(grammar, query)
		if tagErr != nil {
			diagnostics = append(diagnostics, graph.Diagnostic{Path: file.Path, Level: "warning", Message: fmt.Sprintf("Tree-sitter tags query unavailable: %v", tagErr)})
		} else {
			for _, tag := range tagger.TagTree(tree) {
				if strings.HasPrefix(tag.Kind, "definition.") {
					pf.definitions = append(pf.definitions, definitionFromTag(file.Path, entry.Name, tag))
				} else if strings.HasPrefix(tag.Kind, "reference.") {
					// The runtime's dedicated extractors understand selectors,
					// attributes, and receivers more precisely than inferred tags.
					if tag.Kind == "reference.call" && hasUnderstandingHelpers(entry.Name) {
						continue
					}
					pf.references = append(pf.references, referenceFromTag(file.Path, entry.Name, tag))
				}
			}
		}
	}

	for _, span := range gotreesitter.ExtractDefinitionSpans(tree) {
		pf.definitions = append(pf.definitions, definitionFromSpan(file.Path, entry.Name, span, data))
	}
	for _, call := range gotreesitter.ExtractCalls(tree) {
		pf.references = append(pf.references, referenceFromCall(file.Path, entry.Name, call, data))
	}
	pf.heritage = gotreesitter.ExtractHeritage(tree)
	pf.imports = gotreesitter.ExtractImports(tree)
	pf.imports = append(pf.imports, extractAdditionalImports(tree, entry.Name, data)...)
	pf.imports = dedupeImports(pf.imports)
	pf.definitions = dedupeDefinitions(pf.definitions)
	pf.references = dedupeReferences(pf.references)
	return pf, diagnostics, nil
}

var importDeclarationTypes = map[string]map[string]bool{
	"javascript": {"import_statement": true, "export_statement": true},
	"typescript": {"import_statement": true, "export_statement": true},
	"tsx":        {"import_statement": true, "export_statement": true},
	"rust":       {"use_declaration": true},
	"c":          {"preproc_include": true},
	"cpp":        {"preproc_include": true},
	"c_sharp":    {"using_directive": true},
	"swift":      {"import_declaration": true},
	"kotlin":     {"import_header": true},
	"scala":      {"import_declaration": true},
	"dart":       {"import_or_export": true},
	"php":        {"namespace_use_declaration": true},
}

func extractAdditionalImports(tree *gotreesitter.Tree, language string, source []byte) []gotreesitter.ImportRef {
	types := importDeclarationTypes[language]
	if tree == nil || tree.RootNode() == nil || len(types) == 0 {
		return nil
	}
	grammar := tree.Language()
	var refs []gotreesitter.ImportRef
	var walk func(*gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		if types[node.Type(grammar)] {
			if path := importPath(node, grammar, source); path != "" {
				refs = append(refs, gotreesitter.ImportRef{
					Lang: language, Kind: "import", Path: path,
					StartByte: node.StartByte(), EndByte: node.EndByte(),
				})
			}
			return
		}
		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}
	walk(tree.RootNode())
	return refs
}

func importPath(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) string {
	for _, field := range []string{"source", "path", "module", "argument", "name"} {
		if child := node.ChildByFieldName(field, grammar); child != nil {
			if value := cleanImportPath(child.Text(source)); value != "" {
				return value
			}
		}
	}
	var candidate string
	var find func(*gotreesitter.Node)
	find = func(child *gotreesitter.Node) {
		if child == nil || candidate != "" {
			return
		}
		typeName := child.Type(grammar)
		switch typeName {
		case "string", "string_literal", "system_lib_string", "interpreted_string_literal", "raw_string_literal", "scoped_identifier", "identifier", "user_type":
			candidate = cleanImportPath(child.Text(source))
			if candidate != "" {
				return
			}
		}
		for i := 0; i < child.ChildCount(); i++ {
			find(child.Child(i))
		}
	}
	find(node)
	return candidate
}

func cleanImportPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'`<>")
	value = strings.TrimSuffix(value, ";")
	return strings.TrimSpace(value)
}

func dedupeImports(refs []gotreesitter.ImportRef) []gotreesitter.ImportRef {
	seen := map[string]gotreesitter.ImportRef{}
	for _, ref := range refs {
		path := cleanImportPath(ref.Path)
		if path == "" {
			path = cleanImportPath(ref.From)
		}
		if path == "" {
			continue
		}
		ref.Path = path
		key := ref.Kind + "\x00" + path + "\x00" + fmt.Sprintf("%d", ref.StartByte)
		seen[key] = ref
	}
	out := make([]gotreesitter.ImportRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartByte != out[j].StartByte {
			return out[i].StartByte < out[j].StartByte
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func hasUnderstandingHelpers(language string) bool {
	switch language {
	case "go", "java", "python", "starlark", "javascript", "typescript", "tsx":
		return true
	default:
		return false
	}
}

func definitionFromTag(path, language string, tag gotreesitter.Tag) definition {
	kindName := strings.TrimPrefix(tag.Kind, "definition.")
	line := int(tag.Range.StartPoint.Row) + 1
	column := int(tag.NameRange.StartPoint.Column) + 1
	name := cleanName(tag.Name)
	return definition{
		name: name,
		kind: nodeKind(kindName), path: path, language: language,
		startByte: tag.Range.StartByte, endByte: tag.Range.EndByte,
		startLine: line, endLine: int(tag.Range.EndPoint.Row) + 1, column: column,
	}
}

func definitionFromSpan(path, language string, span gotreesitter.DefinitionSpan, source []byte) definition {
	line, column := byteLineColumn(source, span.NameStartByte)
	endLine, _ := byteLineColumn(source, span.EndByte)
	name := cleanName(span.Name)
	return definition{
		name: name,
		kind: nodeKind(span.Kind), path: path, language: language,
		startByte: span.StartByte, endByte: span.EndByte,
		startLine: line, endLine: endLine, column: column,
	}
}

func referenceFromTag(path, language string, tag gotreesitter.Tag) reference {
	return reference{
		name: cleanName(tag.Name), kind: edgeKind(strings.TrimPrefix(tag.Kind, "reference.")),
		path: path, language: language, startByte: tag.Range.StartByte, endByte: tag.Range.EndByte,
		startLine: int(tag.NameRange.StartPoint.Row) + 1, column: int(tag.NameRange.StartPoint.Column) + 1,
	}
}

func referenceFromCall(path, language string, call gotreesitter.CallRef, source []byte) reference {
	line, column := byteLineColumn(source, call.NameStartByte)
	return reference{
		name: cleanName(call.Name), kind: graph.EdgeCalls, path: path, language: language,
		startByte: call.StartByte, endByte: call.EndByte, startLine: line, column: column,
	}
}

func emitDefinitions(files []parsedFile, result *lang.AnalysisResult) {
	for _, file := range files {
		for _, def := range file.definitions {
			if def.name == "" {
				continue
			}
			meta := extractedMeta(def.path, def.startLine, def.language)
			meta["tree_sitter"] = "true"
			packagePath := filepath.ToSlash(filepath.Dir(def.path))
			if packagePath == "." {
				packagePath = ""
			}
			result.Nodes = append(result.Nodes, graph.Node{
				ID: def.id, Kind: def.kind, Name: def.name, Path: def.path,
				Package: packagePath, StartLine: def.startLine, EndLine: def.endLine, Meta: meta,
			})
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(def.path), To: def.id, Meta: meta})
		}
	}
}

func emitReferences(files []parsedFile, result *lang.AnalysisResult) {
	all, local := definitionIndexes(files)
	for _, file := range files {
		for _, ref := range file.references {
			if ref.name == "" {
				continue
			}
			caller := enclosingDefinition(file.definitions, ref.startByte)
			from := graph.FileID(ref.path)
			if caller != nil {
				from = caller.id
			}
			candidates := local[ref.path][normalizeName(ref.name)]
			if len(candidates) != 1 {
				candidates = all[normalizeName(ref.name)]
			}
			meta := extractedMeta(ref.path, ref.startLine, ref.language)
			meta["tree_sitter"] = "true"
			meta["resolved"] = "true"
			to := ""
			if len(candidates) == 1 {
				to = candidates[0].id
				meta["confidence"] = "inferred"
				if candidates[0].path == ref.path {
					meta["rationale"] = "reference name uniquely matches a Tree-sitter definition in the same file"
				} else {
					meta["rationale"] = "reference name uniquely matches a Tree-sitter definition in the analyzed language"
				}
			} else {
				meta["resolved"] = "false"
				kind := graph.NodeType
				scheme := "tree-unresolved-reference"
				if ref.kind == graph.EdgeCalls {
					kind = graph.NodeFunction
					scheme = "tree-unresolved-call"
				}
				to = graph.ContentID(scheme, ref.language, ref.path, fmt.Sprintf("%d", ref.startLine), fmt.Sprintf("%d", ref.column), ref.name)
				result.Nodes = append(result.Nodes, graph.Node{ID: to, Kind: kind, Name: ref.name, Path: ref.path, StartLine: ref.startLine, Meta: meta})
			}
			result.Edges = append(result.Edges, graph.Edge{Kind: ref.kind, From: from, To: to, Meta: meta})
		}
	}
}

func emitHeritage(files []parsedFile, result *lang.AnalysisResult) {
	all, local := definitionIndexes(files)
	for _, file := range files {
		for _, heritage := range file.heritage {
			child := uniqueDefinition(local[file.file.Path][normalizeName(heritage.Name)])
			if child == nil {
				continue
			}
			line, column := byteLineColumn(file.source, heritage.ParentStartByte)
			meta := extractedMeta(file.file.Path, line, file.language)
			meta["tree_sitter"] = "true"
			meta["resolved"] = "true"
			parent := uniqueDefinition(local[file.file.Path][normalizeName(heritage.Parent)])
			if parent == nil {
				parent = uniqueDefinition(all[normalizeName(heritage.Parent)])
			}
			to := ""
			if parent != nil {
				to = parent.id
				meta["confidence"] = "inferred"
				meta["rationale"] = "heritage name uniquely matches a Tree-sitter type definition"
			} else {
				meta["resolved"] = "false"
				to = graph.ContentID("tree-unresolved-type", file.language, file.file.Path, fmt.Sprintf("%d", line), fmt.Sprintf("%d", column), heritage.Parent)
				result.Nodes = append(result.Nodes, graph.Node{ID: to, Kind: graph.NodeType, Name: heritage.Parent, Path: file.file.Path, StartLine: line, Meta: meta})
			}
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeInherits, From: child.id, To: to, Meta: meta})
		}
	}
}

func emitImports(files []parsedFile, result *lang.AnalysisResult) {
	knownFiles := make(map[string]struct{}, len(files))
	for _, file := range files {
		knownFiles[filepath.ToSlash(filepath.Clean(file.file.Path))] = struct{}{}
	}
	for _, file := range files {
		for _, ref := range file.imports {
			name := cleanName(ref.Path)
			if name == "" {
				name = cleanName(ref.From)
			}
			if name == "" || ref.Kind == "package" {
				continue
			}
			line, _ := byteLineColumn(file.source, ref.StartByte)
			meta := extractedMeta(file.file.Path, line, file.language)
			meta["tree_sitter"] = "true"
			meta["import_kind"] = ref.Kind
			if ref.Alias != "" {
				meta["alias"] = ref.Alias
			}
			if localPath := resolveLocalImport(file.file.Path, file.language, name, knownFiles); localPath != "" {
				meta["confidence"] = "inferred"
				meta["resolved"] = "true"
				meta["rationale"] = "import path maps uniquely to an audited source file"
				result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeImports, From: graph.FileID(file.file.Path), To: graph.FileID(localPath), Meta: meta})
				continue
			}
			meta["resolved"] = "false"
			id := graph.ContentID("tree-import", file.language, name)
			result.Nodes = append(result.Nodes, graph.Node{ID: id, Kind: graph.NodeImport, Name: name, Meta: meta})
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeImports, From: graph.FileID(file.file.Path), To: id, Meta: meta})
		}
	}
}

func resolveLocalImport(fromPath, language, importPath string, known map[string]struct{}) string {
	dir := filepath.ToSlash(filepath.Dir(fromPath))
	if dir == "." {
		dir = ""
	}
	var candidates []string
	candidateSet := map[string]struct{}{}
	add := func(path string) {
		path = filepath.ToSlash(filepath.Clean(path))
		path = strings.TrimPrefix(path, "./")
		if _, ok := known[path]; ok {
			if _, duplicate := candidateSet[path]; !duplicate {
				candidateSet[path] = struct{}{}
				candidates = append(candidates, path)
			}
		}
	}
	join := func(base, path string) string {
		if base == "" {
			return path
		}
		return base + "/" + path
	}

	switch language {
	case "javascript", "typescript", "tsx":
		if !strings.HasPrefix(importPath, ".") {
			return ""
		}
		base := filepath.ToSlash(filepath.Clean(join(dir, importPath)))
		add(base)
		if filepath.Ext(base) == "" {
			for _, extension := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
				add(base + extension)
			}
			for _, index := range []string{"index.js", "index.jsx", "index.ts", "index.tsx"} {
				add(base + "/" + index)
			}
		}
	case "python":
		module := strings.TrimLeft(importPath, ".")
		if module == "" {
			return ""
		}
		module = strings.ReplaceAll(module, ".", "/")
		for _, base := range []string{join(dir, module), module} {
			add(base + ".py")
			add(base + "/__init__.py")
		}
	case "rust":
		module := strings.TrimPrefix(importPath, "crate::")
		module = strings.TrimPrefix(module, "self::")
		module = strings.ReplaceAll(module, "::", "/")
		for _, base := range []string{join(dir, module), module, "src/" + module} {
			add(base + ".rs")
			add(base + "/mod.rs")
		}
	case "c", "cpp":
		add(join(dir, importPath))
		add(importPath)
	default:
		return ""
	}

	if len(candidates) != 1 {
		return ""
	}
	return candidates[0]
}

func definitionIndexes(files []parsedFile) (map[string][]definition, map[string]map[string][]definition) {
	all := map[string][]definition{}
	local := map[string]map[string][]definition{}
	for _, file := range files {
		if local[file.file.Path] == nil {
			local[file.file.Path] = map[string][]definition{}
		}
		for _, def := range file.definitions {
			key := normalizeName(def.name)
			all[key] = append(all[key], def)
			local[file.file.Path][key] = append(local[file.file.Path][key], def)
		}
	}
	return all, local
}

func enclosingDefinition(defs []definition, offset uint32) *definition {
	var best *definition
	for i := range defs {
		def := &defs[i]
		if offset < def.startByte || offset >= def.endByte {
			continue
		}
		if best == nil || def.endByte-def.startByte < best.endByte-best.startByte {
			best = def
		}
	}
	return best
}

func uniqueDefinition(defs []definition) *definition {
	if len(defs) != 1 {
		return nil
	}
	return &defs[0]
}

func dedupeDefinitions(defs []definition) []definition {
	seen := map[string]definition{}
	for _, def := range defs {
		if def.name == "" {
			continue
		}
		key := fmt.Sprintf("%d\x00%d\x00%s", def.startByte, def.endByte, normalizeName(def.name))
		if existing, ok := seen[key]; ok {
			if definitionRank(def.kind) > definitionRank(existing.kind) {
				seen[key] = def
			}
			continue
		}
		seen[key] = def
	}
	out := make([]definition, 0, len(seen))
	for _, def := range seen {
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].startByte != out[j].startByte {
			return out[i].startByte < out[j].startByte
		}
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].kind < out[j].kind
	})
	stabilizeDefinitionIDs(out)
	return out
}

// stabilizeDefinitionIDs keeps IDs independent of source line movement. A
// containing type qualifies methods, while true overloads use a deterministic
// source-order ordinal only within the same qualified name.
func stabilizeDefinitionIDs(defs []definition) {
	for i := range defs {
		defs[i].qualified = defs[i].name
		var container *definition
		for j := range defs {
			if i == j || !containerKind(defs[j].kind) {
				continue
			}
			if defs[j].startByte <= defs[i].startByte && defs[j].endByte >= defs[i].endByte {
				if container == nil || defs[j].endByte-defs[j].startByte < container.endByte-container.startByte {
					container = &defs[j]
				}
			}
		}
		if container != nil {
			defs[i].qualified = container.name + "." + defs[i].name
			if defs[i].kind == graph.NodeFunction {
				defs[i].kind = graph.NodeMethod
			}
		}
	}

	counts := map[string]int{}
	for _, def := range defs {
		counts[definitionIdentity(def)]++
	}
	ordinals := map[string]int{}
	for i := range defs {
		identity := definitionIdentity(defs[i])
		ordinal := 0
		if counts[identity] > 1 {
			ordinals[identity]++
			ordinal = ordinals[identity]
		}
		defs[i].id = symbolID(defs[i].language, defs[i].path, defs[i].kind, defs[i].qualified, ordinal)
	}
}

func definitionIdentity(def definition) string {
	return string(def.kind) + "\x00" + def.qualified
}

func containerKind(kind graph.NodeKind) bool {
	switch kind {
	case graph.NodeClass, graph.NodeInterface, graph.NodeStruct:
		return true
	default:
		return false
	}
}

func dedupeReferences(refs []reference) []reference {
	seen := map[string]reference{}
	for _, ref := range refs {
		if ref.name == "" {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", ref.kind, ref.startByte, ref.endByte, normalizeName(ref.name))
		seen[key] = ref
	}
	out := make([]reference, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].startByte != out[j].startByte {
			return out[i].startByte < out[j].startByte
		}
		return out[i].name < out[j].name
	})
	return out
}

func nodeKind(kind string) graph.NodeKind {
	switch strings.ToLower(kind) {
	case "function":
		return graph.NodeFunction
	case "method", "constructor":
		return graph.NodeMethod
	case "class":
		return graph.NodeClass
	case "interface", "trait":
		return graph.NodeInterface
	case "struct", "record":
		return graph.NodeStruct
	case "variable", "constant", "field", "property":
		return graph.NodeVariable
	case "module", "namespace", "package":
		return graph.NodeModule
	default:
		return graph.NodeType
	}
}

func edgeKind(kind string) graph.EdgeKind {
	switch strings.ToLower(kind) {
	case "call", "method", "function":
		return graph.EdgeCalls
	case "type", "class", "interface":
		return graph.EdgeUsesType
	case "implementation":
		return graph.EdgeImplements
	default:
		return graph.EdgeReferences
	}
}

func definitionRank(kind graph.NodeKind) int {
	switch kind {
	case graph.NodeClass, graph.NodeInterface, graph.NodeStruct:
		return 3
	case graph.NodeFunction, graph.NodeMethod:
		return 2
	default:
		return 1
	}
}

func symbolID(language, path string, kind graph.NodeKind, qualified string, ordinal int) string {
	parts := []string{language, path, string(kind), qualified}
	if ordinal > 0 {
		parts = append(parts, fmt.Sprintf("overload-%d", ordinal))
	}
	return graph.ContentID("tree-symbol", parts...)
}

func extractedMeta(path string, line int, language string) map[string]string {
	evidence := path
	if line > 0 {
		evidence += fmt.Sprintf(":%d", line)
	}
	return map[string]string{"confidence": "extracted", "evidence": evidence, "language": language}
}

func cleanName(name string) string {
	return strings.Trim(strings.TrimSpace(name), "\"'`")
}

func normalizeName(name string) string {
	name = cleanName(name)
	if index := strings.LastIndexAny(name, ".:#/"); index >= 0 && index+1 < len(name) {
		name = name[index+1:]
	}
	return name
}

func byteLineColumn(source []byte, offset uint32) (int, int) {
	if uint64(offset) > uint64(len(source)) {
		// #nosec G115 -- the branch proves len(source) is smaller than a uint32.
		offset = uint32(len(source))
	}
	line, column := 1, 1
	for _, b := range source[:offset] {
		if b == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return line, column
}
