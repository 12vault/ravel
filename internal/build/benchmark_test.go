package build

import (
	"context"
	"testing"

	"github.com/12vault/ravel/internal/config"
)

func BenchmarkBuildFixture(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), "../../testdata/simple-go-service", config.Default()); err != nil {
			b.Fatal(err)
		}
	}
}
