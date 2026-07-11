package graph

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type NodeKind string

const (
	NodeRepo      NodeKind = "repo"
	NodeDir       NodeKind = "dir"
	NodeFile      NodeKind = "file"
	NodePackage   NodeKind = "package"
	NodeImport    NodeKind = "import"
	NodeType      NodeKind = "type"
	NodeStruct    NodeKind = "struct"
	NodeInterface NodeKind = "interface"
	NodeFunction  NodeKind = "function"
	NodeMethod    NodeKind = "method"
	NodeVariable  NodeKind = "variable"
	NodeModule    NodeKind = "module"
	NodeClass     NodeKind = "class"
	NodeDocument  NodeKind = "document"
	NodeSection   NodeKind = "section"
	NodeSchema    NodeKind = "schema"
	NodeTable     NodeKind = "table"
	NodeView      NodeKind = "view"
	NodeColumn    NodeKind = "column"
	NodeIndex     NodeKind = "index"
	NodeConcept   NodeKind = "concept"
	NodeDomain    NodeKind = "domain"
	NodeFlow      NodeKind = "flow"
	NodeStep      NodeKind = "step"
	NodeTour      NodeKind = "tour"
)

type EdgeKind string

const (
	EdgeContains   EdgeKind = "contains"
	EdgeImports    EdgeKind = "imports"
	EdgeDefines    EdgeKind = "defines"
	EdgeCalls      EdgeKind = "calls"
	EdgeReferences EdgeKind = "references"
	EdgeImplements EdgeKind = "implements"
	EdgeUsesType   EdgeKind = "uses_type"
	EdgeTestedBy   EdgeKind = "tested_by"
	EdgeInherits   EdgeKind = "inherits"
	EdgeDependsOn  EdgeKind = "depends_on"
	EdgeBelongsTo  EdgeKind = "belongs_to"
	EdgeExplains   EdgeKind = "explains"
	EdgeCites      EdgeKind = "cites"
	EdgeFlowsTo    EdgeKind = "flows_to"
	EdgeAffects    EdgeKind = "affects"
	EdgePartOf     EdgeKind = "part_of"
)

type Node struct {
	ID        string            `json:"id"`
	Kind      NodeKind          `json:"kind"`
	Name      string            `json:"name"`
	Path      string            `json:"path,omitempty"`
	Package   string            `json:"package,omitempty"`
	StartLine int               `json:"startLine,omitempty"`
	EndLine   int               `json:"endLine,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type Edge struct {
	ID   string            `json:"id"`
	Kind EdgeKind          `json:"kind"`
	From string            `json:"from"`
	To   string            `json:"to"`
	Meta map[string]string `json:"meta,omitempty"`
}

type Diagnostic struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Metrics struct {
	NodesByKind map[NodeKind]int `json:"nodesByKind"`
	EdgesByKind map[EdgeKind]int `json:"edgesByKind"`
	Languages   map[string]int   `json:"languages"`
}

type Graph struct {
	Version     string       `json:"version"`
	Root        string       `json:"root"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Nodes       []Node       `json:"nodes"`
	Edges       []Edge       `json:"edges"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Metrics     Metrics      `json:"metrics"`
}

type Builder struct {
	graph Graph
	nodes map[string]Node
	edges map[string]Edge
}

func NewBuilder(root string) *Builder {
	b := &Builder{
		graph: Graph{
			Version:     "0.1",
			Root:        root,
			GeneratedAt: time.Now().UTC(),
			Metrics: Metrics{
				NodesByKind: map[NodeKind]int{},
				EdgesByKind: map[EdgeKind]int{},
				Languages:   map[string]int{},
			},
		},
		nodes: map[string]Node{},
		edges: map[string]Edge{},
	}
	b.AddNode(Node{ID: RepoID(), Kind: NodeRepo, Name: filepath.Base(root), Path: ".", Meta: map[string]string{"confidence": "extracted", "evidence": "."}})
	return b
}

func (b *Builder) AddNode(n Node) {
	if n.ID == "" {
		return
	}
	if existing, ok := b.nodes[n.ID]; ok {
		b.nodes[n.ID] = mergeNode(existing, n)
		return
	}
	b.nodes[n.ID] = n
}

func (b *Builder) AddEdge(e Edge) {
	if e.From == "" || e.To == "" || e.Kind == "" {
		return
	}
	if e.ID == "" {
		e.ID = EdgeID(e.Kind, e.From, e.To)
	}
	if existing, ok := b.edges[e.ID]; ok {
		existing.Meta = mergeMeta(existing.Meta, e.Meta)
		b.edges[e.ID] = existing
		return
	}
	b.edges[e.ID] = e
}

func (b *Builder) AddDiagnostic(d Diagnostic) {
	b.graph.Diagnostics = append(b.graph.Diagnostics, d)
}

func (b *Builder) Build() Graph {
	b.graph.Nodes = make([]Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		b.graph.Nodes = append(b.graph.Nodes, n)
	}
	sort.Slice(b.graph.Nodes, func(i, j int) bool {
		return b.graph.Nodes[i].ID < b.graph.Nodes[j].ID
	})

	b.graph.Edges = make([]Edge, 0, len(b.edges))
	for _, e := range b.edges {
		b.graph.Edges = append(b.graph.Edges, e)
	}
	sort.Slice(b.graph.Edges, func(i, j int) bool {
		return b.graph.Edges[i].ID < b.graph.Edges[j].ID
	})

	b.graph.Metrics = ComputeMetrics(b.graph)
	return b.graph
}

func ComputeMetrics(g Graph) Metrics {
	m := Metrics{
		NodesByKind: map[NodeKind]int{},
		EdgesByKind: map[EdgeKind]int{},
		Languages:   map[string]int{},
	}
	for _, n := range g.Nodes {
		m.NodesByKind[n.Kind]++
		if n.Kind == NodeFile && n.Meta != nil && n.Meta["language"] != "" {
			m.Languages[n.Meta["language"]]++
		}
	}
	for _, e := range g.Edges {
		m.EdgesByKind[e.Kind]++
	}
	return m
}

func RepoID() string {
	return "repo://."
}

func DirID(path string) string {
	if path == "" || path == "." {
		return "dir://."
	}
	return "dir://" + slash(path)
}

func FileID(path string) string {
	return "file://" + slash(path)
}

func PackageID(dir string) string {
	if dir == "" || dir == "." {
		return "go-package://."
	}
	return "go-package://" + slash(dir)
}

func ImportID(path string) string {
	return "go-import://" + path
}

// ExternalFunctionID identifies a function selected from an imported Go
// package. Import nodes describe dependencies; keeping callable symbols in a
// separate namespace prevents call edges from turning package imports into
// artificial call-graph hubs.
func ExternalFunctionID(importPath, name string) string {
	return ContentID("go-external-function", importPath, name)
}

func TypeID(pkgDir, name string) string {
	return "go://" + qualifier(pkgDir) + "." + name
}

func FunctionID(pkgDir, name string) string {
	return "go://" + qualifier(pkgDir) + "." + name
}

func MethodID(pkgDir, receiver, name string) string {
	return "go://" + qualifier(pkgDir) + "." + receiver + "." + name
}

func UnresolvedCallID(name string) string {
	sum := sha1.Sum([]byte(name))
	return "unresolved-call://" + hex.EncodeToString(sum[:8])
}

// UnresolvedCallSiteID identifies one unresolved call occurrence. The caller
// is part of the identity so unrelated unresolved calls with the same spelling
// do not collapse into a high-degree placeholder node.
func UnresolvedCallSiteID(caller, path, name string, line, column int) string {
	identity := strings.Join([]string{
		strings.TrimSpace(caller),
		slash(path),
		fmt.Sprintf("%d", line),
		fmt.Sprintf("%d", column),
		strings.TrimSpace(name),
	}, "\x00")
	sum := sha1.Sum([]byte(identity))
	return "unresolved-callsite://" + hex.EncodeToString(sum[:12])
}

func ContentID(scheme string, parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(filepath.ToSlash(part))
		if part != "" {
			clean = append(clean, part)
		}
	}
	return scheme + "://" + strings.Join(clean, "#")
}

func EdgeID(kind EdgeKind, from, to string) string {
	sum := sha1.Sum([]byte(string(kind) + "\x00" + from + "\x00" + to))
	return string(kind) + "://" + hex.EncodeToString(sum[:12])
}

func SymbolKind(k NodeKind) bool {
	switch k {
	case NodeType, NodeStruct, NodeInterface, NodeFunction, NodeMethod, NodeVariable, NodeModule, NodeClass:
		return true
	default:
		return false
	}
}

func ParentDir(path string) string {
	dir := filepath.Dir(path)
	if dir == "." {
		return "."
	}
	return slash(dir)
}

func qualifier(dir string) string {
	dir = strings.TrimSpace(slash(dir))
	if dir == "" || dir == "." {
		return "root"
	}
	return dir
}

func slash(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func mergeNode(a, b Node) Node {
	if a.Kind == "" {
		a.Kind = b.Kind
	}
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.Path == "" {
		a.Path = b.Path
	}
	if a.Package == "" {
		a.Package = b.Package
	}
	if a.StartLine == 0 {
		a.StartLine = b.StartLine
	}
	if a.EndLine == 0 {
		a.EndLine = b.EndLine
	}
	a.Meta = mergeMeta(a.Meta, b.Meta)
	return a
}

func mergeMeta(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func DisplayName(n Node) string {
	if n.Path != "" && n.Kind == NodeFile {
		return n.Path
	}
	if n.Package != "" && (n.Kind == NodeFunction || n.Kind == NodeMethod || n.Kind == NodeType || n.Kind == NodeStruct || n.Kind == NodeInterface) {
		return fmt.Sprintf("%s.%s", n.Package, n.Name)
	}
	return n.Name
}
