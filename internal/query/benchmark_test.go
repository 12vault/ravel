package query

import (
	"fmt"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func BenchmarkSearchThousandNodes(b *testing.B) {
	builder := graph.NewBuilder(".")
	for i := 0; i < 1000; i++ {
		builder.AddNode(graph.Node{ID: fmt.Sprintf("function://pkg/F%d", i), Kind: graph.NodeFunction, Name: fmt.Sprintf("HandleRequest%d", i), Path: "internal/service.go"})
	}
	g := builder.Build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Search(g, "handle request 500", 25)
	}
}
