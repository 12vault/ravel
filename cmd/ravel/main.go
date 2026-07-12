package main

import (
	"context"
	"os"

	"github.com/12vault/ravel/internal/cli"
)

func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		cli.PrintError(os.Stderr, err)
		os.Exit(1)
	}
}
