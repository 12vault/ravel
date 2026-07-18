package treeanalyzer

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/12vault/ravel/internal/graph"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const (
	declarationChunkLines         = 256
	declarationChunkOverlapLines  = 32
	declarationChunkTimeoutMicros = 500_000
	cFamilyDeclarationRole        = "declaration"
	cFamilyImplementationRole     = "implementation"
)

// supplementalDeclarationKinds closes grammar-specific gaps left by generic
// tags-query inference. The node types are syntax facts from the embedded
// grammars; unknown shapes are skipped instead of guessed.
var supplementalDeclarationKinds = map[string]map[string]graph.NodeKind{
	"javascript": {
		"class_declaration": graph.NodeClass, "class": graph.NodeClass,
		"function_declaration":           graph.NodeFunction,
		"generator_function_declaration": graph.NodeFunction, "method_definition": graph.NodeMethod,
	},
	"typescript": {
		"class_declaration": graph.NodeClass, "class": graph.NodeClass,
		"abstract_class_declaration": graph.NodeClass,
		"interface_declaration":      graph.NodeInterface, "enum_declaration": graph.NodeType,
		"type_alias_declaration": graph.NodeType, "function_declaration": graph.NodeFunction,
		"generator_function_declaration": graph.NodeFunction, "method_definition": graph.NodeMethod,
		"method_signature": graph.NodeMethod,
	},
	"tsx": {
		"class_declaration": graph.NodeClass, "class": graph.NodeClass,
		"abstract_class_declaration": graph.NodeClass,
		"interface_declaration":      graph.NodeInterface, "enum_declaration": graph.NodeType,
		"type_alias_declaration": graph.NodeType, "function_declaration": graph.NodeFunction,
		"generator_function_declaration": graph.NodeFunction, "method_definition": graph.NodeMethod,
		"method_signature": graph.NodeMethod,
	},
	"swift": {
		"class_declaration": graph.NodeClass, "protocol_declaration": graph.NodeInterface,
		"function_declaration": graph.NodeFunction, "protocol_function_declaration": graph.NodeMethod,
		"init_declaration": graph.NodeMethod, "deinit_declaration": graph.NodeMethod,
		"subscript_declaration": graph.NodeMethod,
	},
	"python": {
		"class_definition": graph.NodeClass, "function_definition": graph.NodeFunction,
	},
	"starlark": {
		"class_definition": graph.NodeClass, "function_definition": graph.NodeFunction,
	},
	"java": {
		"class_declaration": graph.NodeClass, "interface_declaration": graph.NodeInterface,
		"enum_declaration": graph.NodeType, "record_declaration": graph.NodeStruct,
		"annotation_type_declaration": graph.NodeInterface, "method_declaration": graph.NodeMethod,
		"constructor_declaration": graph.NodeMethod,
	},
	"kotlin": {
		"class_declaration": graph.NodeClass, "object_declaration": graph.NodeClass,
		"function_declaration": graph.NodeFunction,
	},
	"scala": {
		"class_definition": graph.NodeClass, "object_definition": graph.NodeClass,
		"trait_definition": graph.NodeInterface, "function_definition": graph.NodeFunction,
	},
	"rust": {
		"function_item": graph.NodeFunction, "function_signature_item": graph.NodeMethod,
		"struct_item": graph.NodeStruct, "enum_item": graph.NodeType, "trait_item": graph.NodeInterface,
	},
	"ruby": {
		"class": graph.NodeClass, "module": graph.NodeModule,
		"method": graph.NodeFunction, "singleton_method": graph.NodeMethod,
	},
	"php": {
		"class_declaration": graph.NodeClass, "interface_declaration": graph.NodeInterface,
		"trait_declaration": graph.NodeInterface, "enum_declaration": graph.NodeType,
		"function_definition": graph.NodeFunction, "method_declaration": graph.NodeMethod,
	},
	"c": {"function_definition": graph.NodeFunction},
	"cpp": {
		"class_specifier": graph.NodeClass, "struct_specifier": graph.NodeStruct,
		"enum_specifier": graph.NodeType, "function_definition": graph.NodeFunction,
	},
	"c_sharp": {
		"class_declaration": graph.NodeClass, "interface_declaration": graph.NodeInterface,
		"enum_declaration": graph.NodeType, "struct_declaration": graph.NodeStruct,
		"record_declaration": graph.NodeStruct, "method_declaration": graph.NodeMethod,
		"constructor_declaration": graph.NodeMethod,
	},
	"fsharp": {"function_or_value_defn": graph.NodeFunction},
	"dart": {
		"class_definition": graph.NodeClass, "enum_declaration": graph.NodeType,
		"mixin_declaration": graph.NodeInterface, "extension_declaration": graph.NodeType,
		"function_signature": graph.NodeFunction, "method_signature": graph.NodeMethod,
		"constructor_signature": graph.NodeMethod, "getter_signature": graph.NodeMethod,
		"setter_signature": graph.NodeMethod,
	},
	"erlang": {"fun_decl": graph.NodeFunction, "record_decl": graph.NodeStruct},
	"lua":    {"function_declaration": graph.NodeFunction},
	"objc": {
		"function_definition": graph.NodeFunction, "method_definition": graph.NodeMethod,
		"class_interface": graph.NodeClass, "class_implementation": graph.NodeClass,
		"protocol_declaration": graph.NodeInterface,
	},
	"perl": {"subroutine_declaration_statement": graph.NodeFunction},
	"groovy": {
		"class_declaration": graph.NodeClass, "interface_declaration": graph.NodeInterface,
		"method_declaration": graph.NodeMethod, "constructor_declaration": graph.NodeMethod,
	},
	"solidity": {
		"contract_declaration": graph.NodeClass, "interface_declaration": graph.NodeInterface,
		"library_declaration": graph.NodeModule, "function_definition": graph.NodeFunction,
		"constructor_definition": graph.NodeMethod, "modifier_definition": graph.NodeMethod,
	},
	"bash":       {"function_definition": graph.NodeFunction},
	"powershell": {"function_statement": graph.NodeFunction},
	"proto": {
		"message": graph.NodeStruct, "enum": graph.NodeType, "service": graph.NodeInterface,
		"rpc": graph.NodeMethod,
	},
	"graphql": {
		"object_type_definition": graph.NodeType, "interface_type_definition": graph.NodeInterface,
		"input_object_type_definition": graph.NodeStruct, "enum_type_definition": graph.NodeType,
		"scalar_type_definition": graph.NodeType, "union_type_definition": graph.NodeType,
		"directive_definition": graph.NodeType,
	},
}

var declarationNameNodeTypes = map[string]bool{
	"simple_identifier": true, "identifier": true, "field_identifier": true,
	"property_identifier": true, "type_identifier": true, "name": true,
	"constant": true, "function_name": true, "message_name": true,
	"atom": true, "bareword": true, "word": true, "variable_name": true,
	"sym_name": true, "alias": true,
}

func extractSupplementalDefinitions(tree *gotreesitter.Tree, path, language string, source []byte) []definition {
	if tree == nil || tree.RootNode() == nil || tree.Language() == nil {
		return nil
	}
	grammar := tree.Language()
	var definitions []definition
	var walk func(*gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		if kind, nameNode, name := supplementalDeclaration(node, grammar, language, source); kind != "" && name != "" {
			line, column := byteLineColumn(source, nameNode.StartByte())
			endLine, _ := byteLineColumn(source, node.EndByte())
			def := definition{
				name: cleanName(name), kind: kind, path: path, language: language,
				startByte: node.StartByte(), endByte: node.EndByte(),
				startLine: line, endLine: endLine, column: column,
				partial: node.Type(grammar) == "ERROR",
			}
			def.role, def.qualified, def.signature = cFamilyDeclarationDetails(node, grammar, language, source)
			definitions = append(definitions, def)
		}
		for index := 0; index < node.NamedChildCount(); index++ {
			walk(node.NamedChild(index))
		}
	}
	walk(tree.RootNode())
	return definitions
}

// extractCFamilyGrammarFallback reparses only an errored C/C++ file without
// its optional token-source adapter. Macro-heavy public headers can otherwise
// collapse many prototypes into one oversized pseudo-definition. Every fact
// returned here still comes from a node in the same pinned Tree-sitter grammar.
func extractCFamilyGrammarFallback(ctx context.Context, grammar *gotreesitter.Language, path, language string, source []byte, timeoutMicros uint64) []definition {
	if grammar == nil || len(source) == 0 || (language != "c" && language != "cpp") {
		return nil
	}
	parser := gotreesitter.NewParser(grammar)
	parser.SetTimeoutMicros(timeoutMicros)
	var cancelled uint32
	parser.SetCancellationFlag(&cancelled)
	stop := context.AfterFunc(ctx, func() { atomic.StoreUint32(&cancelled, 1) })
	tree, err := parser.ParseStrict(source)
	stop()
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil
	}
	defer tree.Release()
	definitions := extractSupplementalDefinitions(tree, path, language, source)
	filtered := definitions[:0]
	for _, def := range definitions {
		// The fallback can expose the uppercase wrapper as a pseudo-function
		// alongside the real prototype. Keep the syntax-backed callable, not
		// macro helpers such as GIT_EXTERN or GIT_CALLBACK.
		if (def.kind == graph.NodeFunction || def.kind == graph.NodeMethod) && cFamilyMacroLikeName(def.name) {
			continue
		}
		def.parserMode = "grammar_fallback"
		filtered = append(filtered, def)
	}
	return filtered
}

func cFamilyCallableDefinitionCount(definitions []definition) int {
	count := 0
	for _, def := range definitions {
		if (def.role == cFamilyDeclarationRole || def.role == cFamilyImplementationRole) &&
			(def.kind == graph.NodeFunction || def.kind == graph.NodeMethod) {
			count++
		}
	}
	return count
}

func cFamilyHeaderPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	for _, suffix := range []string{".h", ".hh", ".hpp", ".hxx"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// extractChunkedDeclarations reparses bounded overlapping line windows after a
// whole-file timeout. Each retained fact still comes from a Tree-sitter syntax
// node, but it is marked partial because cross-window relationships are not
// complete.
func extractChunkedDeclarations(ctx context.Context, entry grammars.LangEntry, path string, source []byte) []definition {
	if entry.Language == nil || len(source) == 0 {
		return nil
	}
	grammar := entry.Language()
	if grammar == nil {
		return nil
	}
	offsets := []int{0}
	for index, value := range source {
		if value == '\n' && index+1 < len(source) {
			offsets = append(offsets, index+1)
		}
	}
	step := declarationChunkLines - declarationChunkOverlapLines
	var definitions []definition
	for startLine := 0; startLine < len(offsets); startLine += step {
		if ctx.Err() != nil {
			return definitions
		}
		endLine := min(len(offsets), startLine+declarationChunkLines)
		startByte := offsets[startLine]
		endByte := len(source)
		if endLine < len(offsets) {
			endByte = offsets[endLine]
		}
		fragment := source[startByte:endByte]
		parser := gotreesitter.NewParser(grammar)
		parser.SetTimeoutMicros(declarationChunkTimeoutMicros)
		var cancelled uint32
		parser.SetCancellationFlag(&cancelled)
		stop := context.AfterFunc(ctx, func() { atomic.StoreUint32(&cancelled, 1) })
		var tree *gotreesitter.Tree
		if entry.TokenSourceFactory != nil {
			factory := func(data []byte) (gotreesitter.TokenSource, error) {
				return entry.TokenSourceFactory(data, grammar), nil
			}
			tree, _ = parser.ParseWithTokenSourceFactoryStrict(fragment, factory)
		} else {
			tree, _ = parser.ParseStrict(fragment)
		}
		stop()
		if tree != nil && tree.RootNode() != nil {
			chunk := extractSupplementalDefinitions(tree, path, entry.Name, fragment)
			for index := range chunk {
				chunk[index].startByte += uint32(startByte) // #nosec G115 -- source offsets are bounded by scanner file limits.
				chunk[index].endByte += uint32(startByte)   // #nosec G115 -- source offsets are bounded by scanner file limits.
				chunk[index].startLine += startLine
				chunk[index].endLine += startLine
				chunk[index].partial = true
			}
			definitions = append(definitions, chunk...)
			tree.Release()
		} else if tree != nil {
			tree.Release()
		}
		if endLine == len(offsets) {
			break
		}
	}
	return definitions
}

func supplementalDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, language string, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	nodeType := node.Type(grammar)
	if language == "c" || language == "cpp" || language == "objc" {
		if kind, nameNode, name := cFamilyFunctionDeclaration(node, grammar, source); kind != "" {
			return kind, nameNode, name
		}
	}
	if (language == "javascript" || language == "typescript" || language == "tsx") && nodeType == "variable_declarator" {
		return javascriptVariableDeclaration(node, grammar, source)
	}
	if (language == "typescript" || language == "tsx") && nodeType == "ERROR" {
		return typescriptRecoveredDeclaration(node, grammar, source)
	}
	if kind := supplementalDeclarationKinds[language][nodeType]; kind != "" {
		nameNode := declarationNameNode(node, grammar, language, nodeType)
		if nameNode == nil {
			switch nodeType {
			case "init_declaration":
				return kind, node, "init"
			case "deinit_declaration":
				return kind, node, "deinit"
			case "subscript_declaration":
				return kind, node, "subscript"
			}
			return "", nil, ""
		}
		if language == "fsharp" && nodeType == "function_or_value_defn" && !hasDescendantType(node, grammar, "argument_patterns") {
			kind = graph.NodeVariable
		}
		if language == "swift" && nodeType == "class_declaration" {
			kind = swiftDeclarationKind(node.Text(source))
		}
		return kind, nameNode, nameNode.Text(source)
	}

	switch language {
	case "elixir":
		return elixirDeclaration(node, grammar, source)
	case "clojure":
		return clojureDeclaration(node, grammar, source)
	case "r":
		return rDeclaration(node, grammar, source)
	case "hcl":
		return hclDeclaration(node, grammar, source)
	}
	return "", nil, ""
}

func cFamilyFunctionDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	switch node.Type(grammar) {
	case "function_definition", "declaration", "field_declaration":
	default:
		return "", nil, ""
	}
	declarator := cFamilyFunctionDeclarator(node, grammar)
	if declarator == nil || cFamilyFunctionPointer(declarator, grammar, source) {
		return "", nil, ""
	}
	nameNode := cFamilyDeclaratorNameNode(declarator, grammar)
	if nameNode == nil {
		return "", nil, ""
	}
	return graph.NodeFunction, nameNode, nameNode.Text(source)
}

func cFamilyDeclarationDetails(node *gotreesitter.Node, grammar *gotreesitter.Language, language string, source []byte) (string, string, string) {
	if language != "c" && language != "cpp" && language != "objc" {
		return "", "", ""
	}
	role := ""
	switch node.Type(grammar) {
	case "function_definition":
		role = cFamilyImplementationRole
	case "declaration", "field_declaration":
		role = cFamilyDeclarationRole
	default:
		return "", "", ""
	}
	declarator := cFamilyFunctionDeclarator(node, grammar)
	if declarator == nil || cFamilyFunctionPointer(declarator, grammar, source) {
		return "", "", ""
	}
	nameNode := cFamilyDeclaratorNameNode(declarator, grammar)
	if nameNode == nil {
		return "", "", ""
	}
	qualified := nameNode.Text(source)
	if root := declarator.ChildByFieldName("declarator", grammar); root != nil {
		scopedTypes := map[string]bool{
			"qualified_identifier": true, "scoped_identifier": true,
		}
		scoped := root
		if !scopedTypes[root.Type(grammar)] {
			scoped = firstDescendantByTypes(root, grammar, scopedTypes)
		}
		if scoped != nil {
			qualified = scoped.Text(source)
		}
	}
	return role, qualified, cFamilyFunctionSignature(declarator, grammar, source)
}
func cFamilyMacroLikeName(name string) bool {
	name = strings.TrimSpace(name)
	if !strings.ContainsRune(name, '_') || name != strings.ToUpper(name) {
		return false
	}
	for _, character := range name {
		if character >= 'A' && character <= 'Z' {
			return true
		}
	}
	return false
}

func cFamilyFunctionDeclarator(node *gotreesitter.Node, grammar *gotreesitter.Language) *gotreesitter.Node {
	var find func(*gotreesitter.Node, bool) *gotreesitter.Node
	find = func(current *gotreesitter.Node, blocked bool) *gotreesitter.Node {
		if current == nil {
			return nil
		}
		switch current.Type(grammar) {
		case "macro_type_specifier", "type_descriptor", "parameter_declaration", "optional_parameter_declaration":
			blocked = true
		case "function_declarator":
			if !blocked {
				return current
			}
		}
		for index := 0; index < current.NamedChildCount(); index++ {
			if found := find(current.NamedChild(index), blocked); found != nil {
				return found
			}
		}
		return nil
	}
	return find(node, false)
}

func firstDescendantByType(node *gotreesitter.Node, grammar *gotreesitter.Language, wanted string) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	if node.Type(grammar) == wanted {
		return node
	}
	for index := 0; index < node.NamedChildCount(); index++ {
		if found := firstDescendantByType(node.NamedChild(index), grammar, wanted); found != nil {
			return found
		}
	}
	return nil
}

func cFamilyDeclaratorNameNode(declarator *gotreesitter.Node, grammar *gotreesitter.Language) *gotreesitter.Node {
	root := declarator.ChildByFieldName("declarator", grammar)
	if root == nil {
		root = declarator
	}
	for current := root; current != nil; {
		if declarationNameNodeTypes[current.Type(grammar)] || current.Type(grammar) == "operator_name" || current.Type(grammar) == "destructor_name" {
			return current
		}
		if child := current.ChildByFieldName("name", grammar); child != nil {
			current = child
			continue
		}
		if child := current.ChildByFieldName("declarator", grammar); child != nil {
			current = child
			continue
		}
		break
	}
	types := make(map[string]bool, len(declarationNameNodeTypes)+2)
	for name := range declarationNameNodeTypes {
		types[name] = true
	}
	types["operator_name"] = true
	types["destructor_name"] = true
	return lastDescendantByTypes(root, grammar, types)
}

func cFamilyFunctionPointer(declarator *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) bool {
	root := declarator.ChildByFieldName("declarator", grammar)
	if root == nil {
		return false
	}
	text := strings.Join(strings.Fields(root.Text(source)), "")
	return strings.Contains(text, "(*") || strings.Contains(text, "(&")
}

func cFamilyFunctionSignature(declarator *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) string {
	parameters := declarator.ChildByFieldName("parameters", grammar)
	if parameters == nil {
		parameters = firstDescendantByType(declarator, grammar, "parameter_list")
	}
	if parameters == nil {
		return ""
	}
	parts := make([]string, 0, parameters.NamedChildCount())
	for index := 0; index < parameters.NamedChildCount(); index++ {
		parameter := parameters.NamedChild(index)
		if parameter == nil || parameter.Type(grammar) == "comment" {
			continue
		}
		text := parameter.Text(source)
		if nameRoot := parameter.ChildByFieldName("declarator", grammar); nameRoot != nil {
			if nameNode := cFamilyDeclaratorNameNode(nameRoot, grammar); nameNode != nil &&
				nameNode.StartByte() >= parameter.StartByte() && nameNode.EndByte() <= parameter.EndByte() {
				start := int(nameNode.StartByte() - parameter.StartByte())
				end := int(nameNode.EndByte() - parameter.StartByte())
				if start >= 0 && end >= start && end <= len(text) {
					text = text[:start] + text[end:]
				}
			}
		}
		if equals := strings.IndexByte(text, '='); equals >= 0 {
			text = text[:equals]
		}
		if normalized := normalizeCFamilySignatureText(text); normalized != "" {
			parts = append(parts, normalized)
		}
	}
	if len(parts) == 1 && parts[0] == "void" {
		parts = nil
	}
	suffix := ""
	if parameters.EndByte() <= declarator.EndByte() {
		start, end := int(parameters.EndByte()), int(declarator.EndByte())
		if start >= 0 && end >= start && end <= len(source) {
			suffix = normalizeCFamilySignatureText(string(source[start:end]))
			for _, marker := range []string{"override", "final", "=0", "=default", "=delete"} {
				suffix = strings.ReplaceAll(suffix, marker, "")
			}
		}
	}
	return "(" + strings.Join(parts, ",") + ")" + suffix
}

func normalizeCFamilySignatureText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), "")
}

// Some valid TypeScript forms such as
// `class Service extends Context.Service<Service, Shape>()("service") {}`
// are recovered as an ERROR node by the embedded grammar. Keep a declaration
// only when the recovered node still contains a direct identifier preceded by
// a declaration keyword. Marking ERROR-backed definitions partial keeps their
// provenance honest.
func typescriptRecoveredDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child == nil || (child.Type(grammar) != "identifier" && child.Type(grammar) != "type_identifier") {
			continue
		}
		start, end := int(node.StartByte()), int(child.StartByte())
		if start < 0 || end < start || end > len(source) {
			return "", nil, ""
		}
		prefix := strings.Fields(string(source[start:end]))
		if len(prefix) == 0 {
			return "", nil, ""
		}
		kind := graph.NodeKind("")
		switch prefix[len(prefix)-1] {
		case "class":
			kind = graph.NodeClass
		case "function":
			kind = graph.NodeFunction
		case "const", "let", "var":
			kind = graph.NodeVariable
			if hasDescendantType(node, grammar, "arrow_function") || hasDescendantType(node, grammar, "function_expression") {
				kind = graph.NodeFunction
			}
		default:
			return "", nil, ""
		}
		return kind, child, child.Text(source)
	}
	return "", nil, ""
}

// javascriptVariableDeclaration keeps named module-level bindings while
// avoiding the much larger set of function-local temporaries. Arrow and
// function expressions are callable declarations; other bindings are
// variables. Every emitted fact is backed by a variable_declarator node.
func javascriptVariableDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	nameNode := node.ChildByFieldName("name", grammar)
	if nameNode == nil || nameNode.Type(grammar) != "identifier" || !javascriptModuleBinding(node, grammar) {
		return "", nil, ""
	}
	kind := graph.NodeVariable
	if value := node.ChildByFieldName("value", grammar); value != nil {
		switch value.Type(grammar) {
		case "arrow_function", "function_expression", "generator_function":
			kind = graph.NodeFunction
		}
	}
	return kind, nameNode, nameNode.Text(source)
}

func javascriptModuleBinding(node *gotreesitter.Node, grammar *gotreesitter.Language) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	switch parent.Type(grammar) {
	case "lexical_declaration", "variable_declaration":
	default:
		return false
	}
	return javascriptModuleNode(parent, grammar)
}

func javascriptModuleNode(node *gotreesitter.Node, grammar *gotreesitter.Language) bool {
	parent := node.Parent()
	if parent != nil && parent.Type(grammar) == "export_statement" {
		parent = parent.Parent()
	}
	return parent != nil && parent.Type(grammar) == "program"
}

func declarationNameNode(node *gotreesitter.Node, grammar *gotreesitter.Language, language, nodeType string) *gotreesitter.Node {
	if child := node.ChildByFieldName("name", grammar); child != nil {
		return child
	}
	if (language == "c" || language == "cpp" || language == "objc") && nodeType == "function_definition" {
		if declarator := node.ChildByFieldName("declarator", grammar); declarator != nil {
			return lastDescendantByTypes(declarator, grammar, declarationNameNodeTypes)
		}
	}
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if declarationNameNodeTypes[child.Type(grammar)] {
			return child
		}
	}
	return firstDescendantByTypes(node, grammar, declarationNameNodeTypes)
}

func firstDescendantByTypes(node *gotreesitter.Node, grammar *gotreesitter.Language, types map[string]bool) *gotreesitter.Node {
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if types[child.Type(grammar)] {
			return child
		}
		if found := firstDescendantByTypes(child, grammar, types); found != nil {
			return found
		}
	}
	return nil
}

func lastDescendantByTypes(node *gotreesitter.Node, grammar *gotreesitter.Language, types map[string]bool) *gotreesitter.Node {
	var found *gotreesitter.Node
	if types[node.Type(grammar)] {
		found = node
	}
	for index := 0; index < node.NamedChildCount(); index++ {
		if child := lastDescendantByTypes(node.NamedChild(index), grammar, types); child != nil {
			found = child
		}
	}
	return found
}

func hasDescendantType(node *gotreesitter.Node, grammar *gotreesitter.Language, wanted string) bool {
	if node.Type(grammar) == wanted {
		return true
	}
	for index := 0; index < node.NamedChildCount(); index++ {
		if hasDescendantType(node.NamedChild(index), grammar, wanted) {
			return true
		}
	}
	return false
}

func swiftDeclarationKind(source string) graph.NodeKind {
	header := strings.TrimSpace(source)
	if body := strings.IndexByte(header, '{'); body >= 0 {
		header = header[:body]
	}
	bestIndex := len(header) + 1
	bestKind := graph.NodeType
	for _, prefix := range []struct {
		keyword string
		kind    graph.NodeKind
	}{
		{"struct ", graph.NodeStruct}, {"enum ", graph.NodeType},
		{"actor ", graph.NodeClass}, {"protocol ", graph.NodeInterface},
		{"class ", graph.NodeClass}, {"extension ", graph.NodeType},
	} {
		if index := strings.Index(header, prefix.keyword); index >= 0 && index < bestIndex {
			bestIndex = index
			bestKind = prefix.kind
		}
	}
	return bestKind
}

func elixirDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	if node.Type(grammar) != "call" || node.NamedChildCount() < 2 {
		return "", nil, ""
	}
	keywordNode := node.NamedChild(0)
	keyword := strings.TrimSpace(keywordNode.Text(source))
	kind := graph.NodeKind("")
	switch keyword {
	case "def", "defp", "defmacro", "defmacrop", "defguard", "defguardp":
		kind = graph.NodeFunction
	case "defmodule":
		kind = graph.NodeModule
	case "defprotocol":
		kind = graph.NodeInterface
	case "defstruct":
		kind = graph.NodeStruct
	default:
		return "", nil, ""
	}
	nameNode := firstDescendantByTypes(node.NamedChild(1), grammar, declarationNameNodeTypes)
	if nameNode == nil {
		return "", nil, ""
	}
	return kind, nameNode, nameNode.Text(source)
}

func clojureDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	if node.Type(grammar) != "list_lit" || node.NamedChildCount() < 2 {
		return "", nil, ""
	}
	keyword := strings.TrimSpace(node.NamedChild(0).Text(source))
	kind := graph.NodeKind("")
	switch keyword {
	case "defn", "defn-", "defmacro":
		kind = graph.NodeFunction
	case "defprotocol":
		kind = graph.NodeInterface
	case "defrecord":
		kind = graph.NodeStruct
	case "deftype", "defmulti":
		kind = graph.NodeType
	default:
		return "", nil, ""
	}
	nameNode := firstDescendantByTypes(node.NamedChild(1), grammar, declarationNameNodeTypes)
	if nameNode == nil {
		return "", nil, ""
	}
	return kind, nameNode, nameNode.Text(source)
}

func rDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	if node.Type(grammar) != "binary_operator" || node.NamedChildCount() < 2 {
		return "", nil, ""
	}
	left, right := node.NamedChild(0), node.NamedChild(1)
	if right.Type(grammar) != "function_definition" || left.Type(grammar) != "identifier" {
		return "", nil, ""
	}
	return graph.NodeFunction, left, left.Text(source)
}

func hclDeclaration(node *gotreesitter.Node, grammar *gotreesitter.Language, source []byte) (graph.NodeKind, *gotreesitter.Node, string) {
	if node.Type(grammar) != "block" || node.NamedChildCount() == 0 {
		return "", nil, ""
	}
	var nameNode *gotreesitter.Node
	for index := 1; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child.Type(grammar) == "string_lit" {
			nameNode = child
		}
	}
	if nameNode == nil {
		nameNode = node.NamedChild(0)
	}
	return graph.NodeModule, nameNode, nameNode.Text(source)
}
