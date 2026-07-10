package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	buildrunner "github.com/12ya/reporavel/internal/build"
	"github.com/12ya/reporavel/internal/config"
	gitHooks "github.com/12ya/reporavel/internal/hooks"
	installmgr "github.com/12ya/reporavel/internal/install"
	"github.com/12ya/reporavel/internal/query"
	"github.com/12ya/reporavel/internal/report"
	"github.com/12ya/reporavel/internal/scan"
	"github.com/12ya/reporavel/internal/security"
	"github.com/12ya/reporavel/internal/store"
)

var Version = "v0.1.0"

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "reporavel %s\n", Version)
		return nil
	case "init":
		return runInit(args[1:], stdout)
	case "install":
		return runInstall(args[1:], stdout)
	case "uninstall":
		return runUninstall(args[1:], stdout)
	case "codex":
		return runCodex(args[1:], stdout)
	case "hook":
		return runHook(args[1:], stdout)
	case "assistant-hook":
		return runAssistantHook(stdout)
	case "doctor":
		return runDoctor(args[1:], stdout)
	case "audit", "scan":
		return runAudit(args[1:], stdout)
	case "build":
		return runBuild(ctx, args[1:], stdout)
	case "report":
		return runReport(args[1:], stdout)
	case "query":
		return runQuery(args[1:], stdout)
	case "explain":
		return runExplain(args[1:], stdout)
	case "path":
		return runPath(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInstall(args []string, stdout io.Writer) error {
	fs := newFlagSet("install")
	platform := fs.String("platform", "claude", "AI assistant platform")
	project := fs.Bool("project", false, "install into the current project")
	if err := fs.Parse(flexibleFlags(args, "platform")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("install does not accept positional arguments")
	}
	dst, err := installmgr.InstallSkill(installmgr.SkillOptions{Platform: *platform, Project: *project})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Skill installed: %s\n", dst)
	if *project {
		if strings.EqualFold(*platform, "codex") {
			paths, err := installmgr.InstallCodex(installmgr.CodexOptions{})
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Codex integration installed: %s\n", strings.Join(paths, ", "))
		}
		fmt.Fprintf(stdout, "Add to version control: git add %s\n", dst)
	}
	fmt.Fprintln(stdout, "Invoke it from your assistant as $reporavel (Codex) or /reporavel.")
	return nil
}

func runUninstall(args []string, stdout io.Writer) error {
	fs := newFlagSet("uninstall")
	platform := fs.String("platform", "claude", "AI assistant platform")
	project := fs.Bool("project", false, "remove the current-project installation")
	if err := fs.Parse(flexibleFlags(args, "platform")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("uninstall does not accept positional arguments")
	}
	dst, removed, err := installmgr.UninstallSkill(installmgr.SkillOptions{Platform: *platform, Project: *project})
	if err != nil {
		return err
	}
	if *project && strings.EqualFold(*platform, "codex") {
		if _, err := installmgr.UninstallCodex("."); err != nil {
			return err
		}
	}
	if removed {
		fmt.Fprintf(stdout, "Skill removed: %s\n", dst)
	} else {
		fmt.Fprintf(stdout, "Skill not installed: %s\n", dst)
	}
	return nil
}

func runCodex(args []string, stdout io.Writer) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		return errors.New("usage: reporavel codex <install|uninstall>")
	}
	if args[0] == "install" {
		paths, err := installmgr.InstallCodex(installmgr.CodexOptions{})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Codex integration installed: %s\n", strings.Join(paths, ", "))
		return nil
	}
	paths, err := installmgr.UninstallCodex(".")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Codex integration removed from: %s\n", strings.Join(paths, ", "))
	return nil
}

func runHook(args []string, stdout io.Writer) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("usage: reporavel hook <install|uninstall|status> [root]")
	}
	root := "."
	if len(args) == 2 {
		root = args[1]
	}
	switch args[0] {
	case "install":
		dir, err := gitHooks.Install(root, "")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Git hooks installed: %s\n", dir)
		return nil
	case "uninstall":
		dir, err := gitHooks.Uninstall(root)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Git hooks removed: %s\n", dir)
		return nil
	case "status":
		status, err := gitHooks.Check(root)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "post-commit: %s\n", installedLabel(status.PostCommit))
		fmt.Fprintf(stdout, "post-checkout: %s\n", installedLabel(status.PostCheckout))
		return nil
	default:
		return errors.New("usage: reporavel hook <install|uninstall|status> [root]")
	}
}

func runAssistantHook(stdout io.Writer) error {
	data, err := installmgr.AssistantHook(".")
	if err != nil {
		return err
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(stdout, string(data))
	}
	return err
}

func installedLabel(installed bool) string {
	if installed {
		return "installed"
	}
	return "not installed"
}

func PrintError(w io.Writer, err error) {
	fmt.Fprintf(w, "reporavel: %v\n", err)
}

func runInit(args []string, stdout io.Writer) error {
	fs := newFlagSet("init")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	if err := fs.Parse(flexibleFlags(args, "config")); err != nil {
		return err
	}
	if err := config.WriteDefault(*configPath); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote %s\n", *configPath)
	return nil
}

func runDoctor(args []string, stdout io.Writer) error {
	fs := newFlagSet("doctor")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	if err := fs.Parse(flexibleFlags(args, "config")); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	security.WriteDoctor(stdout, cfg)
	return nil
}

func runAudit(args []string, stdout io.Writer) error {
	fs := newFlagSet("audit")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	outDir := fs.String("out", "", "output directory")
	maxFileSize := fs.Int64("max-file-size", 0, "max file size in bytes")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "max-file-size")); err != nil {
		return err
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	cfg, err := loadConfigWithOverrides(*configPath, *outDir, *maxFileSize)
	if err != nil {
		return err
	}
	result, err := scan.Scan(root, cfg)
	if err != nil {
		return err
	}
	writeAudit(stdout, result, cfg)
	return nil
}

func runBuild(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("build")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	outDir := fs.String("out", "", "output directory")
	maxFileSize := fs.Int64("max-file-size", 0, "max file size in bytes")
	noCallGraph := fs.Bool("no-call-graph", false, "disable AST call extraction")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "max-file-size")); err != nil {
		return err
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	cfg, err := loadConfigWithOverrides(*configPath, *outDir, *maxFileSize)
	if err != nil {
		return err
	}
	if *noCallGraph {
		cfg.Analysis.CallGraph = false
	}
	result, err := buildrunner.Run(ctx, root, cfg)
	if err != nil {
		return err
	}
	out := cfg.Output.Dir
	if !filepath.IsAbs(out) {
		out = filepath.Join(result.Scan.Root, out)
	}
	md := report.Markdown(result.Graph)
	if err := store.WriteArtifacts(out, result.Graph, result.Scan, md, cfg.Output); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote %s\n", out)
	fmt.Fprintf(stdout, "Files analyzed: %d\n", len(result.Scan.Files))
	fmt.Fprintf(stdout, "Nodes: %d\n", len(result.Graph.Nodes))
	fmt.Fprintf(stdout, "Edges: %d\n", len(result.Graph.Edges))
	return nil
}

func runReport(args []string, stdout io.Writer) error {
	fs := newFlagSet("report")
	outDir := fs.String("out", ".reporavel", "output directory")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	reportPath := filepath.Join(*outDir, "report.md")
	if data, err := os.ReadFile(reportPath); err == nil {
		_, err = stdout.Write(data)
		return err
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, report.Markdown(g))
	return err
}

func runQuery(args []string, stdout io.Writer) error {
	fs := newFlagSet("query")
	outDir := fs.String("out", ".reporavel", "output directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	limit := fs.Int("limit", 25, "max results")
	if err := fs.Parse(flexibleFlags(args, "out", "limit")); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("query requires search text")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	results := query.Search(g, strings.Join(fs.Args(), " "), *limit)
	return query.WriteSearch(stdout, results, *jsonOut)
}

func runExplain(args []string, stdout io.Writer) error {
	fs := newFlagSet("explain")
	outDir := fs.String("out", ".reporavel", "output directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("explain requires a file, symbol, or node id")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	ex, ok := query.Explain(g, strings.Join(fs.Args(), " "))
	if !ok {
		return errors.New("target not found")
	}
	return query.WriteExplanation(stdout, ex, *jsonOut)
}

func runPath(args []string, stdout io.Writer) error {
	fs := newFlagSet("path")
	outDir := fs.String("out", ".reporavel", "output directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("path requires exactly two targets")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	nodes, ok := query.ShortestPath(g, fs.Arg(0), fs.Arg(1))
	if !ok {
		fmt.Fprintln(stdout, "No path found.")
		return nil
	}
	return query.WritePath(stdout, nodes, *jsonOut)
}

func loadConfigWithOverrides(configPath, outDir string, maxFileSize int64) (config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return cfg, err
	}
	if outDir != "" {
		cfg.Output.Dir = outDir
	}
	if maxFileSize > 0 {
		cfg.Scan.MaxFileSizeBytes = maxFileSize
	}
	return cfg, nil
}

func writeAudit(w io.Writer, result scan.Result, cfg config.Config) {
	fmt.Fprintln(w, "RepoRavel Audit")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Root: %s\n", result.Root)
	fmt.Fprintf(w, "Mode: %s\n", cfg.Mode)
	fmt.Fprintln(w, "Network: disabled")
	fmt.Fprintln(w, "Shell execution: disabled")
	fmt.Fprintln(w, "Secret files: ignored")
	fmt.Fprintf(w, "Output: %s\n", cfg.Output.Dir)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Will analyze:")
	counts := map[string]int{}
	for _, f := range result.Files {
		counts[f.Language]++
	}
	if len(counts) == 0 {
		fmt.Fprintln(w, "- No supported files found")
	} else {
		for _, row := range sortedCounts(counts) {
			fmt.Fprintf(w, "- %s: %d files\n", row.Key, row.Count)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Estimated read size: %d bytes\n", result.TotalBytes)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Will ignore:")
	if len(result.Ignored) == 0 {
		fmt.Fprintln(w, "- Nothing")
		return
	}
	limit := len(result.Ignored)
	if limit > 30 {
		limit = 30
	}
	for i := 0; i < limit; i++ {
		ignored := result.Ignored[i]
		fmt.Fprintf(w, "- %s (%s)\n", ignored.Path, ignored.Reason)
	}
	if len(result.Ignored) > limit {
		fmt.Fprintf(w, "- ... %d more\n", len(result.Ignored)-limit)
	}
}

type countRow struct {
	Key   string
	Count int
}

func sortedCounts(counts map[string]int) []countRow {
	rows := make([]countRow, 0, len(counts))
	for k, v := range counts {
		rows = append(rows, countRow{Key: k, Count: v})
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].Count > rows[i].Count || (rows[j].Count == rows[i].Count && rows[j].Key < rows[i].Key) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	return rows
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func flexibleFlags(args []string, valueFlags ...string) []string {
	needsValue := map[string]bool{}
	for _, name := range valueFlags {
		needsValue["-"+name] = true
		needsValue["--"+name] = true
	}
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name := arg
			if before, _, ok := strings.Cut(arg, "="); ok {
				name = before
			}
			if needsValue[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "RepoRavel %s\n", Version)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  reporavel version")
	fmt.Fprintln(w, "  reporavel init")
	fmt.Fprintln(w, "  reporavel install [--platform <name>] [--project]")
	fmt.Fprintln(w, "  reporavel uninstall [--platform <name>] [--project]")
	fmt.Fprintln(w, "  reporavel codex <install|uninstall>")
	fmt.Fprintln(w, "  reporavel hook <install|uninstall|status> [root]")
	fmt.Fprintln(w, "  reporavel doctor")
	fmt.Fprintln(w, "  reporavel audit [root]")
	fmt.Fprintln(w, "  reporavel build [root]")
	fmt.Fprintln(w, "  reporavel report")
	fmt.Fprintln(w, "  reporavel query [--json] <text>")
	fmt.Fprintln(w, "  reporavel explain [--json] <file-or-symbol>")
	fmt.Fprintln(w, "  reporavel path [--json] <from> <to>")
}
