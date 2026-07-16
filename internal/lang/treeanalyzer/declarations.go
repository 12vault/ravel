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
)

// supplementalDeclarationKinds closes grammar-specific gaps left by generic
// tags-query inference. The node types are syntax facts from the embedded
// grammars; unknown shapes are skipped instead of guessed.
var supplementalDeclarationKinds = map[string]map[string]graph.NodeKind{
	"javascript": {
		"class_declaration": graph.NodeClass, "function_declaration": graph.NodeFunction,
		"generator_function_declaration": graph.NodeFunction, "method_definition": graph.NodeMethod,
	},
	"typescript": {
		"class_declaration": graph.NodeClass, "abstract_class_declaration": graph.NodeClass,
		"interface_declaration": graph.NodeInterface, "enum_declaration": graph.NodeType,
		"type_alias_declaration": graph.NodeType, "function_declaration": graph.NodeFunction,
		"generator_function_declaration": graph.NodeFunction, "method_definition": graph.NodeMethod,
		"method_signature": graph.NodeMethod,
	},
	"tsx": {
		"class_declaration": graph.NodeClass, "abstract_class_declaration": graph.NodeClass,
		"interface_declaration": graph.NodeInterface, "enum_declaration": graph.NodeType,
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
			definitions = append(definitions, definition{
				name: cleanName(name), kind: kind, path: path, language: language,
				startByte: node.StartByte(), endByte: node.EndByte(),
				startLine: line, endLine: endLine, column: column,
			})
		}
		for index := 0; index < node.NamedChildCount(); index++ {
			walk(node.NamedChild(index))
		}
	}
	walk(tree.RootNode())
	return definitions
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
