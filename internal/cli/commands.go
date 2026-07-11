package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	buildrunner "github.com/12ya/reporavel/internal/build"
	"github.com/12ya/reporavel/internal/config"
	"github.com/12ya/reporavel/internal/corpus"
	"github.com/12ya/reporavel/internal/dashboard"
	"github.com/12ya/reporavel/internal/evaluation"
	gitHooks "github.com/12ya/reporavel/internal/hooks"
	"github.com/12ya/reporavel/internal/ingest"
	installmgr "github.com/12ya/reporavel/internal/install"
	"github.com/12ya/reporavel/internal/orchestrate"
	"github.com/12ya/reporavel/internal/query"
	"github.com/12ya/reporavel/internal/report"
	"github.com/12ya/reporavel/internal/scan"
	"github.com/12ya/reporavel/internal/security"
	"github.com/12ya/reporavel/internal/selfupdate"
	"github.com/12ya/reporavel/internal/share"
	"github.com/12ya/reporavel/internal/store"
	updater "github.com/12ya/reporavel/internal/update"
	"github.com/12ya/reporavel/internal/workflow"
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
		fmt.Fprintf(stdout, "ravel %s\n", Version)
		return nil
	case "self-update":
		return runSelfUpdate(ctx, args[1:], stdout)
	case "init":
		return runInit(args[1:], stdout)
	case "install":
		return runInstall(args[1:], stdout)
	case "uninstall":
		return runUninstall(args[1:], stdout)
	case "codex":
		return runCodex(args[1:], stdout)
	case "claude", "cursor", "vscode", "copilot", "gemini", "opencode":
		return runPlatformIntegration(args[0], args[1:], stdout)
	case "hook":
		return runHook(args[1:], stdout)
	case "assistant-hook":
		return runAssistantHook(args[1:], stdout)
	case "ingest":
		return runIngest(args[1:], stdout)
	case "dashboard":
		return runDashboard(args[1:], stdout)
	case "doctor":
		return runDoctor(args[1:], stdout)
	case "tools":
		return runTools(args[1:], stdout)
	case "extract":
		return runExtract(ctx, args[1:], stdout)
	case "plan":
		return runPlan(args[1:], stdout)
	case "benchmark":
		return runBenchmark(args[1:], stdout)
	case "audit", "scan":
		return runAudit(args[1:], stdout)
	case "build":
		return runBuild(ctx, args[1:], stdout)
	case "update":
		return runUpdate(ctx, args[1:], stdout)
	case "watch":
		return runWatch(ctx, args[1:], stdout)
	case "share":
		return runShare(args[1:], stdout)
	case "report":
		return runReport(args[1:], stdout)
	case "query":
		return runQuery(args[1:], stdout)
	case "explain":
		return runExplain(args[1:], stdout)
	case "path":
		return runPath(args[1:], stdout)
	case "tech", "understand", "learn", "docs", "pdf", "schema", "diff":
		return runWorkflow(args[0], args[1:], stdout)
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
		if paths, err := installmgr.InstallIntegration(installmgr.IntegrationOptions{Platform: *platform}); err == nil {
			fmt.Fprintf(stdout, "%s integration installed: %s\n", *platform, strings.Join(paths, ", "))
		} else if strings.Contains(err.Error(), "native project integration is unavailable") {
			fmt.Fprintf(stdout, "No native %s integration; installed its portable skill bundle.\n", *platform)
		} else {
			return err
		}
		fmt.Fprintf(stdout, "Add to version control: git add %s\n", dst)
	}
	fmt.Fprintln(stdout, "Invoke it from your assistant as $ravel (Codex) or /ravel.")
	return nil
}

func runSelfUpdate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("self-update")
	version := fs.String("version", "latest", "release version or latest")
	repository := fs.String("repo", "12vault/ravel", "GitHub owner/repository")
	platforms := fs.String("platforms", "", "comma-separated skill platforms to refresh")
	project := fs.Bool("project", false, "refresh project-scoped skills and integrations")
	if err := fs.Parse(flexibleFlags(args, "version", "repo", "platforms")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("self-update does not accept positional arguments")
	}
	path, err := selfupdate.Run(ctx, selfupdate.Options{Version: *version, Repository: *repository})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updated Ravel binary: %s\n", path)
	for _, platform := range strings.Split(*platforms, ",") {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		installArgs := []string{"install", "--platform", platform}
		if *project {
			installArgs = append(installArgs, "--project")
		}
		command := exec.CommandContext(ctx, path, installArgs...)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("refresh %s skill: %w: %s", platform, err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(stdout, "Refreshed %s skill and integration\n", platform)
	}
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
	if *project {
		if _, err := installmgr.UninstallIntegration(*platform, "."); err != nil && !strings.Contains(err.Error(), "native project integration is unavailable") {
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
	return runPlatformIntegration("codex", args, stdout)
}

func runPlatformIntegration(platform string, args []string, stdout io.Writer) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		return fmt.Errorf("usage: ravel %s <install|uninstall>", platform)
	}
	if args[0] == "install" {
		paths, err := installmgr.InstallIntegration(installmgr.IntegrationOptions{Platform: platform})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s integration installed: %s\n", platform, strings.Join(paths, ", "))
		return nil
	}
	paths, err := installmgr.UninstallIntegration(platform, ".")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s integration removed from: %s\n", platform, strings.Join(paths, ", "))
	return nil
}

func runHook(args []string, stdout io.Writer) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("usage: ravel hook <install|uninstall|status> [root]")
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
		return errors.New("usage: ravel hook <install|uninstall|status> [root]")
	}
}

func runAssistantHook(args []string, stdout io.Writer) error {
	fs := newFlagSet("assistant-hook")
	platform := fs.String("platform", "codex", "assistant platform")
	if err := fs.Parse(flexibleFlags(args, "platform")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("assistant-hook does not accept positional arguments")
	}
	data, err := installmgr.AssistantHook(".", *platform)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(stdout, string(data))
	}
	return err
}

func runIngest(args []string, stdout io.Writer) error {
	fs := newFlagSet("ingest")
	outDir := fs.String("out", ".reporavel", "output directory")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("ingest requires exactly one graph fragment file")
	}
	current, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	fragment, err := ingest.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	merged, err := ingest.Apply(current, fragment)
	if err != nil {
		return err
	}
	if err := store.RewriteGraphViews(*outDir, merged, report.Markdown(merged)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Ingested %d nodes and %d edges from %s\n", len(fragment.Nodes), len(fragment.Edges), fragment.Source)
	return nil
}

func runDashboard(args []string, stdout io.Writer) error {
	fs := newFlagSet("dashboard")
	outDir := fs.String("out", ".reporavel", "output directory")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("dashboard does not accept positional arguments")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	path := filepath.Join(*outDir, "graph.html")
	if err := dashboard.Write(path, g); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote %s\n", path)
	return nil
}

func installedLabel(installed bool) string {
	if installed {
		return "installed"
	}
	return "not installed"
}

func PrintError(w io.Writer, err error) {
	fmt.Fprintf(w, "ravel: %v\n", err)
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

func runUpdate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("update")
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
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	out := cfg.Output.Dir
	if !filepath.IsAbs(out) {
		out = filepath.Join(absRoot, out)
	}
	previous, err := store.LoadGraph(out)
	if err != nil {
		return fmt.Errorf("update requires an existing graph: %w", err)
	}
	previousScan, err := store.LoadScan(out)
	if err != nil {
		return fmt.Errorf("update requires existing file metadata: %w", err)
	}
	result, err := updater.Run(ctx, root, cfg, previous, previousScan)
	if err != nil {
		return err
	}
	markdown := report.Markdown(result.Build.Graph)
	if err := store.WriteArtifacts(out, result.Build.Graph, result.Build.Scan, markdown, cfg.Output); err != nil {
		return err
	}
	if err := store.WriteChanges(out, result.Changed, result.Removed); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updated %s\n", out)
	fmt.Fprintf(stdout, "Changed files: %d\n", len(result.Changed))
	fmt.Fprintf(stdout, "Removed files: %d\n", len(result.Removed))
	return nil
}

func runWatch(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("watch")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	outDir := fs.String("out", "", "output directory")
	interval := fs.Duration("interval", 2*time.Second, "polling interval")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "interval")); err != nil {
		return err
	}
	if *interval <= 0 {
		return errors.New("watch interval must be positive")
	}
	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	} else if fs.NArg() > 1 {
		return errors.New("watch accepts at most one root")
	}
	cfg, err := loadConfigWithOverrides(*configPath, *outDir, 0)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	out := cfg.Output.Dir
	if !filepath.IsAbs(out) {
		out = filepath.Join(absRoot, out)
	}
	previous, err := store.LoadScan(out)
	if err != nil {
		return fmt.Errorf("watch requires an existing graph: %w", err)
	}
	fmt.Fprintf(stdout, "Watching %s every %s\n", absRoot, interval.String())
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current, err := scan.Scan(root, cfg)
			if err != nil {
				return err
			}
			if sameScan(previous, current) {
				continue
			}
			updateArgs := []string{"--config", *configPath}
			if *outDir != "" {
				updateArgs = append(updateArgs, "--out", *outDir)
			}
			updateArgs = append(updateArgs, root)
			if err := runUpdate(ctx, updateArgs, stdout); err != nil {
				return err
			}
			previous = current
		}
	}
}

func sameScan(a, b scan.Result) bool {
	if len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i].Path != b.Files[i].Path || a.Files[i].Hash != b.Files[i].Hash {
			return false
		}
	}
	return true
}

func runShare(args []string, stdout io.Writer) error {
	fs := newFlagSet("share")
	from := fs.String("from", ".reporavel", "source graph directory")
	out := fs.String("out", "ravel-graph", "share bundle directory")
	if err := fs.Parse(flexibleFlags(args, "from", "out")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("share does not accept positional arguments")
	}
	g, err := store.LoadGraph(*from)
	if err != nil {
		return err
	}
	if err := share.Write(*out, g, time.Now()); err != nil {
		return err
	}
	if err := share.Validate(*out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote commit-safe graph bundle to %s\n", *out)
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

func runWorkflow(mode string, args []string, stdout io.Writer) error {
	fs := newFlagSet(mode)
	outDir := fs.String("out", ".reporavel", "output directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if mode != "diff" && fs.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", mode)
	}
	targets := fs.Args()
	if mode == "diff" && len(targets) == 0 {
		changes, err := store.LoadChanges(*outDir)
		if err != nil {
			return errors.New("diff requires changed paths or a prior ravel update")
		}
		targets = append(changes.Changed, changes.Removed...)
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	view, err := workflow.Build(mode, g, targets)
	if err != nil {
		return err
	}
	return workflow.Write(stdout, view, *jsonOut)
}

func runPlan(args []string, stdout io.Writer) error {
	fs := newFlagSet("plan")
	outDir := fs.String("out", ".reporavel", "graph and task output directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	batchSize := fs.Int("batch-size", 40, "maximum source paths per code task")
	if err := fs.Parse(flexibleFlags(args, "out", "batch-size")); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("plan requires a route")
	}
	route := fs.Arg(0)
	targets := fs.Args()[1:]
	if route == "diff" && len(targets) == 0 {
		changes, err := store.LoadChanges(*outDir)
		if err != nil {
			return errors.New("diff plan requires changed paths or a prior ravel update")
		}
		targets = append(changes.Changed, changes.Removed...)
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	plan, err := orchestrate.Build(route, g, targets, *batchSize, *outDir)
	if err != nil {
		return err
	}
	return orchestrate.Write(stdout, plan, *jsonOut)
}

func runTools(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("tools does not accept arguments")
	}
	fmt.Fprintln(stdout, "Ravel local tool discovery")
	for _, tool := range []struct {
		name, purpose string
	}{
		{"pdftotext", "PDF text extraction"},
		{"mutool", "PDF text and metadata extraction"},
		{"pandoc", "document conversion"},
		{"sqlite3", "SQLite schema inspection"},
		{"psql", "PostgreSQL schema inspection"},
		{"mysqldump", "MySQL schema inspection"},
	} {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			fmt.Fprintf(stdout, "missing\t%s\t%s\n", tool.name, tool.purpose)
			continue
		}
		fmt.Fprintf(stdout, "available\t%s\t%s\t%s\n", tool.name, tool.purpose, path)
	}
	return nil
}

func runExtract(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("extract")
	graphDir := fs.String("graph", ".reporavel", "graph directory")
	out := fs.String("out", ".reporavel/corpus", "local extracted-text directory")
	jsonOut := fs.Bool("json", false, "write manifest JSON")
	if err := fs.Parse(flexibleFlags(args, "graph", "out")); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("extract requires one or more audited corpus paths")
	}
	g, err := store.LoadGraph(*graphDir)
	if err != nil {
		return err
	}
	manifest, err := corpus.Extract(ctx, g, *out, fs.Args(), corpus.ExecRunner{})
	if err != nil {
		return err
	}
	if *jsonOut {
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(data))
		return err
	}
	for _, result := range manifest.Results {
		fmt.Fprintf(stdout, "Extracted %s with %s -> %s\n", result.Source, result.Tool, result.Output)
	}
	return nil
}

func runBenchmark(args []string, stdout io.Writer) error {
	fs := newFlagSet("benchmark")
	graphDir := fs.String("graph", ".reporavel", "graph directory")
	dataset := fs.String("dataset", "", "evaluation JSONL file")
	out := fs.String("out", "-", "result JSON file or - for stdout")
	topK := fs.Int("top-k", 10, "retrieval result count")
	if err := fs.Parse(flexibleFlags(args, "graph", "dataset", "out", "top-k")); err != nil {
		return err
	}
	if fs.NArg() != 0 || *dataset == "" {
		return errors.New("benchmark requires --dataset <cases.jsonl>")
	}
	g, err := store.LoadGraph(*graphDir)
	if err != nil {
		return err
	}
	cases, err := evaluation.LoadJSONL(*dataset)
	if err != nil {
		return err
	}
	result, err := evaluation.Run(g, cases, *topK)
	if err != nil {
		return err
	}
	if *out == "-" {
		return evaluation.Write(stdout, result)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	file, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := evaluation.Write(file, result); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote benchmark results to %s\n", *out)
	return nil
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
	fmt.Fprintln(w, "  ravel version")
	fmt.Fprintln(w, "  ravel self-update [--version latest] [--platforms codex,claude] [--project]")
	fmt.Fprintln(w, "  ravel init")
	fmt.Fprintln(w, "  ravel install [--platform <name>] [--project]")
	fmt.Fprintln(w, "  ravel uninstall [--platform <name>] [--project]")
	fmt.Fprintln(w, "  ravel codex|claude|cursor|vscode|gemini|opencode <install|uninstall>")
	fmt.Fprintln(w, "  ravel hook <install|uninstall|status> [root]")
	fmt.Fprintln(w, "  ravel ingest [--out <dir>] <fragment.json>")
	fmt.Fprintln(w, "  ravel dashboard [--out <dir>]")
	fmt.Fprintln(w, "  ravel doctor")
	fmt.Fprintln(w, "  ravel tools")
	fmt.Fprintln(w, "  ravel extract [--json] <audited-doc-or-pdf>...")
	fmt.Fprintln(w, "  ravel plan [--json] <route> [paths...]")
	fmt.Fprintln(w, "  ravel benchmark --dataset <cases.jsonl> [--graph .reporavel]")
	fmt.Fprintln(w, "  ravel audit [root]")
	fmt.Fprintln(w, "  ravel build [root]")
	fmt.Fprintln(w, "  ravel update [root]")
	fmt.Fprintln(w, "  ravel watch [--interval 2s] [root]")
	fmt.Fprintln(w, "  ravel share [--out ravel-graph]")
	fmt.Fprintln(w, "  ravel report")
	fmt.Fprintln(w, "  ravel query [--json] <text>")
	fmt.Fprintln(w, "  ravel explain [--json] <file-or-symbol>")
	fmt.Fprintln(w, "  ravel path [--json] <from> <to>")
	fmt.Fprintln(w, "  ravel tech|understand|learn|docs|pdf|schema [--json]")
	fmt.Fprintln(w, "  ravel diff [--json] <changed-path>...")
}
