package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/12vault/ravel/internal/cli"
	"github.com/12vault/ravel/internal/install"
	"github.com/12vault/ravel/internal/lang/treeanalyzer"
)

func main() {
	args := os.Args[1:]
	refreshExistingInstallations(args)
	if err := cli.Execute(context.Background(), args, os.Stdout, os.Stderr); err != nil {
		cli.PrintError(os.Stderr, err)
		os.Exit(1)
	}
}

func refreshExistingInstallations(args []string) {
	if len(args) == 0 || args[0] == treeanalyzer.InternalWorkerCommand || autoRefreshDisabled() {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	// go run executables disappear when their temporary build directory is
	// cleaned up. Never let one drive persistent installation maintenance.
	if strings.Contains(filepath.ToSlash(executable), "/go-build") {
		return
	}
	if _, err := install.RefreshExisting(install.RefreshOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "ravel: warning: refresh existing integrations: %v\n", err)
	}
}

func autoRefreshDisabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("RAVEL_NO_AUTO_REFRESH")))
	return value == "1" || value == "true" || value == "yes"
}
