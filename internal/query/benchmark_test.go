package query

import (
	"fmt"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func BenchmarkSearchThousandNodes(b *testing.B) {
	builder := graph.NewBuilder(".")
	for i := 0; i < 1000; i++ {
		builder.AddNode(graph.Node{ID: fmt.Sprintf("function://pkg/F%d", i), Kind: graph.NodeFunction, Name: fmt.Sprintf("HandleRequest%d", i), Path: "internal/service.go"})
	}
	g := builder.Build()
	index := NewIndex(g)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index.Search("handle request 500", 25)
	}
}

func BenchmarkBuildIndexThousandNodes(b *testing.B) {
	builder := graph.NewBuilder(".")
	for i := 0; i < 1000; i++ {
		builder.AddNode(graph.Node{ID: fmt.Sprintf("function://pkg/F%d", i), Kind: graph.NodeFunction, Name: fmt.Sprintf("HandleRequest%d", i), Path: "internal/service.go"})
	}
	g := builder.Build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewIndex(g)
	}
}

func BenchmarkRetrieveThousandNodes(b *testing.B) {
	builder := graph.NewBuilder(".")
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("function://pkg/F%d", i)
		builder.AddNode(graph.Node{ID: id, Kind: graph.NodeFunction, Name: fmt.Sprintf("HandleRequest%d", i), Path: "internal/service.go"})
		if i > 0 {
			builder.AddEdge(graph.Edge{Kind: graph.EdgeCalls, From: fmt.Sprintf("function://pkg/F%d", i-1), To: id})
		}
	}
	index := NewIndex(builder.Build())
	for _, traversal := range []Traversal{TraversalBFS, TraversalDFS} {
		b.Run(string(traversal), func(b *testing.B) {
			options := RetrieveOptions{Traversal: traversal, Direction: DirectionBoth, MaxDepth: 3, MaxNodes: 100, TokenBudget: 2000, DisableRelationInference: true}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := index.Retrieve("handle request 500", options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
