package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	buildrunner "github.com/12vault/ravel/internal/build"
	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/corpus"
	"github.com/12vault/ravel/internal/dashboard"
	"github.com/12vault/ravel/internal/evaluation"
	"github.com/12vault/ravel/internal/graph"
	gitHooks "github.com/12vault/ravel/internal/hooks"
	"github.com/12vault/ravel/internal/ingest"
	installmgr "github.com/12vault/ravel/internal/install"
	"github.com/12vault/ravel/internal/lang/treeanalyzer"
	"github.com/12vault/ravel/internal/mcp"
	"github.com/12vault/ravel/internal/orchestrate"
	pranalysis "github.com/12vault/ravel/internal/prs"
	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/report"
	"github.com/12vault/ravel/internal/scan"
	"github.com/12vault/ravel/internal/security"
	"github.com/12vault/ravel/internal/selfupdate"
	"github.com/12vault/ravel/internal/share"
	"github.com/12vault/ravel/internal/store"
	updater "github.com/12vault/ravel/internal/update"
	"github.com/12vault/ravel/internal/workflow"
	"github.com/12vault/ravel/internal/workspace"
)

var Version = "v0.2.6"

var checkForUpdate = selfupdate.Check

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return ExecuteIO(ctx, args, os.Stdin, stdout, stderr)
}

// ExecuteIO is the injectable CLI entry point used by long-running stdio
// integrations. Execute remains the compatibility wrapper for normal callers.
func ExecuteIO(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}
	if args[0] == "help" {
		if len(args) == 1 {
			usage(stdout)
			return nil
		}
		if len(args) != 2 {
			return errors.New("help accepts at most one command")
		}
		return commandUsage(stdout, args[1])
	}
	if commandHelpRequested(args[1:]) {
		return commandUsage(stdout, args[0])
	}
	switch args[0] {
	case "-h", "--help":
		usage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "ravel %s\n", Version)
		return nil
	case treeanalyzer.InternalWorkerCommand:
		if len(args) != 1 {
			return errors.New("tree analyzer worker does not accept arguments")
		}
		return treeanalyzer.RunProcessWorker(ctx, stdin, stdout)
	case "self-update":
		return runSelfUpdate(ctx, args[1:], stdout)
	case "update-check":
		return runUpdateCheck(ctx, args[1:], stdout)
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
	case "community":
		return runCommunity(args[1:], stdout)
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
		return runAudit(args[1:], stdout, stderr)
	case "build":
		return runBuild(ctx, args[1:], stdout, stderr)
	case "update":
		return runUpdate(ctx, args[1:], stdout, stderr)
	case "watch":
		return runWatch(ctx, args[1:], stdout)
	case "share":
		return runShare(args[1:], stdout)
	case "merge":
		return runMerge(args[1:], stdout)
	case "global":
		return runGlobal(args[1:], stdout)
	case "prs":
		return runPRs(ctx, args[1:], stdout)
	case "report":
		return runReport(args[1:], stdout)
	case "query":
		return runQuery(args[1:], stdout)
	case "context":
		return runContext(args[1:], stdout)
	case "context-batch":
		return runContextBatch(ctx, args[1:], stdin, stdout)
	case "affected":
		return runAffected(args[1:], stdout)
	case "explain":
		return runExplain(args[1:], stdout)
	case "path":
		return runPath(args[1:], stdout)
	case "mcp":
		return runMCP(ctx, args[1:], stdin, stdout)
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
	writeInstallSuccess(stdout, dst, terminalSupportsColor(stdout))
	return nil
}

func writeInstallSuccess(w io.Writer, destination string, color bool) {
	const (
		cyan  = "\x1b[38;2;0;194;199m"
		coral = "\x1b[38;2;255;92;77m"
		dim   = "\x1b[2m"
		reset = "\x1b[0m"
	)

	paint := func(code, value string) string {
		if !color {
			return value
		}
		return code + value + reset
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, paint(cyan, "    ●────────╮"))
	fmt.Fprintln(w, paint(cyan, "             │"))
	fmt.Fprintln(w, paint(cyan, "    ●─────   ╰●"))
	fmt.Fprintf(w, "%s%s\n", paint(cyan, "    │          "), paint(coral, "╲"))
	fmt.Fprintf(w, "%s%s\n", paint(cyan, "    │           "), paint(coral, "╲"))
	fmt.Fprintf(w, "%s%s\n", paint(cyan, "    ● · · · · · "), paint(coral, "●"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s   %s\n", paint(cyan, "    █▀▀▄  ▄▀▀▄  █   █  █▀▀▀  █"), paint(dim, Version))
	fmt.Fprintln(w, paint(cyan, "    █▀▀▄  █▄▄█  ▀▄ ▄▀  █▀▀   █"))
	fmt.Fprintln(w, paint(cyan, "    ▀  ▀  ▀  ▀   ▀▄▀   ▀▀▀▀  ▀▀▀▀"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "    skill installed  →  %s\n", destination)
	fmt.Fprintln(w, "    local CLI ready  →  ravel")
	fmt.Fprintln(w, "    network access   →  none")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    Done. Ask your coding assistant:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "      /ravel understand")
}

func terminalSupportsColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
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

func runUpdateCheck(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("update-check")
	repository := fs.String("repo", "12vault/ravel", "GitHub owner/repository")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(flexibleFlags(args, "repo")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("update-check does not accept positional arguments")
	}
	result, err := checkForUpdate(ctx, selfupdate.CheckOptions{CurrentVersion: Version, Repository: *repository})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	if !result.UpdateAvailable {
		fmt.Fprintf(stdout, "Ravel is up to date: %s\n", result.CurrentVersion)
		return nil
	}
	fmt.Fprintf(stdout, "Ravel %s is available; current version is %s.\n", result.LatestVersion, result.CurrentVersion)
	fmt.Fprintln(stdout, "Run: ravel self-update")
	fmt.Fprintf(stdout, "Release: %s\n", result.ReleaseURL)
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

func runCommunity(args []string, stdout io.Writer) error {
	describe := len(args) > 0 && args[0] == "describe"
	if describe {
		args = args[1:]
	}
	configPath := flagValue(args, "config", ".reporavel.yaml")
	cfg, err := loadCommandConfig(args, configPath)
	if err != nil {
		return err
	}
	fs := newFlagSet("community")
	configFlag := fs.String("config", configPath, "configuration file")
	outDir := fs.String("out", cfg.Output.Dir, "graph directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	template := fs.Bool("template", false, "write an AI-description input template")
	granularity := fs.String("granularity", cfg.Output.CommunityGranularity, "coarse, balanced, or fine")
	hubThreshold := fs.Int("hub-degree-threshold", cfg.Output.CommunityHubDegreeThreshold, "0 for automatic, -1 to disable")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "granularity", "hub-degree-threshold")); err != nil {
		return err
	}
	if *configFlag != configPath {
		return errors.New("internal config flag parsing mismatch")
	}
	preset, err := community.ParsePreset(*granularity)
	if err != nil {
		return err
	}
	if *hubThreshold < -1 {
		return errors.New("community hub degree threshold must be -1, 0, or positive")
	}
	options := community.Options{Granularity: preset, HubDegreeThreshold: *hubThreshold}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	previous := g
	g = community.AssignWithOptions(g, options)
	g = community.RemapLabels(g, previous)
	if describe {
		if *template || *jsonOut || fs.NArg() != 1 {
			return errors.New("community describe requires exactly one descriptions JSON file")
		}
		descriptions, err := community.LoadDescriptions(fs.Arg(0))
		if err != nil {
			return err
		}
		g, err = community.ApplyDescriptions(g, descriptions)
		if err != nil {
			return err
		}
		markdown := report.MarkdownWithCommunityOptions(g, true, options)
		if err := store.RewriteGraphViewsConfigured(*outDir, g, markdown, options); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Applied %d inferred community descriptions from %s\n", len(descriptions.Descriptions), descriptions.Source)
		return nil
	}
	if fs.NArg() != 0 {
		return errors.New("community does not accept positional arguments (use community describe <file>)")
	}
	summaries := community.Summaries(g)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if *template {
		file := community.DescriptionFile{Version: 1, Source: "community-describer"}
		for _, summary := range summaries {
			file.Descriptions = append(file.Descriptions, community.Description{Community: summary.ID})
		}
		return encoder.Encode(file)
	}
	if *jsonOut {
		return encoder.Encode(summaries)
	}
	for _, summary := range summaries {
		fmt.Fprintf(stdout, "%s\t%s\t%d nodes", summary.ID, summary.Name, summary.Size)
		if summary.Description != "" {
			fmt.Fprintf(stdout, "\t%s", summary.Description)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func runDashboard(args []string, stdout io.Writer) error {
	configPath := flagValue(args, "config", ".reporavel.yaml")
	cfg, err := loadCommandConfig(args, configPath)
	if err != nil {
		return err
	}
	fs := newFlagSet("dashboard")
	configFlag := fs.String("config", configPath, "configuration file")
	outDir := fs.String("out", cfg.Output.Dir, "output directory")
	communities := fs.Bool("communities", cfg.Output.CommunityClustering, "detect and display graph communities")
	granularity := fs.String("community-granularity", cfg.Output.CommunityGranularity, "coarse, balanced, or fine")
	hubThreshold := fs.Int("community-hub-degree-threshold", cfg.Output.CommunityHubDegreeThreshold, "0 for automatic, -1 to disable")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "community-granularity", "community-hub-degree-threshold")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("dashboard does not accept positional arguments")
	}
	if *configFlag != configPath {
		return errors.New("internal config flag parsing mismatch")
	}
	preset, err := community.ParsePreset(*granularity)
	if err != nil {
		return err
	}
	if *hubThreshold < -1 {
		return errors.New("community hub degree threshold must be -1, 0, or positive")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	path := filepath.Join(*outDir, "graph.html")
	if err := dashboard.WriteConfigured(path, g, *communities, community.Options{Granularity: preset, HubDegreeThreshold: *hubThreshold}); err != nil {
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

func runAudit(args []string, stdout, progressOutput io.Writer) error {
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
	progress := newTraversalProgress(progressOutput)
	defer progress.Close()
	result, err := scan.ScanWithProgress(root, cfg, progress.Scan)
	if err != nil {
		return err
	}
	progress.Close()
	writeAudit(stdout, result, cfg)
	return nil
}

func runBuild(ctx context.Context, args []string, stdout, progressOutput io.Writer) error {
	fs := newFlagSet("build")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	outDir := fs.String("out", "", "output directory")
	maxFileSize := fs.Int64("max-file-size", 0, "max file size in bytes")
	noCallGraph := fs.Bool("no-call-graph", false, "disable AST call extraction")
	jobs := fs.Int("jobs", 0, "maximum concurrent analysis workers")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "max-file-size", "jobs")); err != nil {
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
	if flagWasSet(fs, "jobs") && (*jobs < 1 || *jobs > 256) {
		return fmt.Errorf("--jobs must be between 1 and 256")
	}
	if flagWasSet(fs, "jobs") {
		cfg.Analysis.Jobs = *jobs
	}
	progress := newTraversalProgress(progressOutput)
	defer progress.Close()
	result, err := buildrunner.RunWithCache(ctx, root, cfg, progress.Build, buildrunner.CacheOptions{OutputDir: cfg.Output.Dir, Version: Version})
	if err != nil {
		return err
	}
	out := cfg.Output.Dir
	if !filepath.IsAbs(out) {
		out = filepath.Join(result.Scan.Root, out)
	}
	if cfg.Output.CommunityClustering {
		result.Graph = community.AssignWithProgress(result.Graph, communityOptions(cfg.Output), func(event community.Progress) {
			progress.Build(buildrunner.Progress{Stage: event.Stage, Path: event.Path, Completed: event.Completed, Total: event.Total, Unit: event.Unit})
		})
	} else {
		result.Graph = community.Remove(result.Graph)
	}
	communityCount := graphCommunityCount(result.Graph)
	progress.Build(buildrunner.Progress{Stage: "Generating report", Path: fmt.Sprintf("%d communities", communityCount), Completed: len(result.Graph.Nodes), Unit: "nodes", Secondary: len(result.Graph.Edges), SecondaryUnit: "edges"})
	md := report.MarkdownPrepared(result.Graph, cfg.Output.CommunityClustering)
	artifactsWritten := 0
	if err := store.WritePreparedArtifactsWithProgress(out, result.Graph, result.Scan, md, cfg.Output, func(event store.ArtifactProgress) {
		artifactsWritten = event.Completed
		progress.Build(buildrunner.Progress{Stage: "Writing artifacts", Path: event.Path, Completed: event.Completed, Total: event.Total, Unit: "artifacts"})
	}); err != nil {
		return err
	}
	progress.Close()
	fmt.Fprintf(stdout, "Wrote %s\n", out)
	fmt.Fprintf(stdout, "Files scanned: %d\n", len(result.Scan.Files))
	fmt.Fprintf(stdout, "Files graphified: %d\n", result.Graph.Metrics.NodesByKind[graph.NodeFile])
	if len(result.Skipped) > 0 {
		fmt.Fprintf(stdout, "Warning: %d file(s) produced no graph content and were skipped\n", len(result.Skipped))
	}
	fmt.Fprintf(stdout, "Nodes: %d\n", len(result.Graph.Nodes))
	fmt.Fprintf(stdout, "Edges: %d\n", len(result.Graph.Edges))
	fmt.Fprintf(stdout, "Communities: %d\n", communityCount)
	fmt.Fprintf(stdout, "Artifacts written: %d\n", artifactsWritten)
	return nil
}

func runUpdate(ctx context.Context, args []string, stdout, progressOutput io.Writer) error {
	fs := newFlagSet("update")
	configPath := fs.String("config", ".reporavel.yaml", "config path")
	outDir := fs.String("out", "", "output directory")
	maxFileSize := fs.Int64("max-file-size", 0, "max file size in bytes")
	noCallGraph := fs.Bool("no-call-graph", false, "disable AST call extraction")
	jobs := fs.Int("jobs", 0, "maximum concurrent analysis workers")
	if err := fs.Parse(flexibleFlags(args, "config", "out", "max-file-size", "jobs")); err != nil {
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
	if flagWasSet(fs, "jobs") && (*jobs < 1 || *jobs > 256) {
		return fmt.Errorf("--jobs must be between 1 and 256")
	}
	if flagWasSet(fs, "jobs") {
		cfg.Analysis.Jobs = *jobs
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
	progress := newTraversalProgress(progressOutput)
	defer progress.Close()
	result, err := updater.RunWithCache(ctx, root, cfg, previous, previousScan, progress.Build, buildrunner.CacheOptions{OutputDir: cfg.Output.Dir, Version: Version})
	if err != nil {
		return err
	}
	if !cfg.Output.CommunityClustering {
		result.Build.Graph = community.Remove(result.Build.Graph)
	}
	communityCount := graphCommunityCount(result.Build.Graph)
	progress.Build(buildrunner.Progress{Stage: "Generating report", Path: fmt.Sprintf("%d communities", communityCount), Completed: len(result.Build.Graph.Nodes), Unit: "nodes", Secondary: len(result.Build.Graph.Edges), SecondaryUnit: "edges"})
	markdown := report.MarkdownPrepared(result.Build.Graph, cfg.Output.CommunityClustering)
	artifactsWritten := 0
	if err := store.WritePreparedArtifactsWithProgress(out, result.Build.Graph, result.Build.Scan, markdown, cfg.Output, func(event store.ArtifactProgress) {
		artifactsWritten = event.Completed
		progress.Build(buildrunner.Progress{Stage: "Writing artifacts", Path: event.Path, Completed: event.Completed, Total: event.Total, Unit: "artifacts"})
	}); err != nil {
		return err
	}
	progress.Build(buildrunner.Progress{Stage: "Writing changes", Completed: len(result.Build.Scan.Files), Total: len(result.Build.Scan.Files)})
	if err := store.WriteChanges(out, result.Changed, result.Removed); err != nil {
		return err
	}
	progress.Close()
	fmt.Fprintf(stdout, "Updated %s\n", out)
	fmt.Fprintf(stdout, "Changed files: %d\n", len(result.Changed))
	fmt.Fprintf(stdout, "Removed files: %d\n", len(result.Removed))
	if len(result.Build.Skipped) > 0 {
		fmt.Fprintf(stdout, "Warning: %d file(s) produced no graph content and were skipped\n", len(result.Build.Skipped))
	}
	fmt.Fprintf(stdout, "Nodes: %d\n", len(result.Build.Graph.Nodes))
	fmt.Fprintf(stdout, "Edges: %d\n", len(result.Build.Graph.Edges))
	fmt.Fprintf(stdout, "Communities: %d\n", communityCount)
	fmt.Fprintf(stdout, "Artifacts written: %d\n", artifactsWritten+1)
	return nil
}

func graphCommunityCount(g graph.Graph) int {
	ids := map[string]bool{}
	for _, node := range g.Nodes {
		if id := node.Meta[community.MetaKey]; id != "" {
			ids[id] = true
		}
	}
	return len(ids)
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
			if err := runUpdate(ctx, updateArgs, stdout, stdout); err != nil {
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

func runMerge(args []string, stdout io.Writer) error {
	fs := newFlagSet("merge")
	outDir := fs.String("out", ".ravel-workspace", "merged graph directory")
	if err := fs.Parse(flexibleFlags(args, "out")); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("merge requires at least one alias=graph-directory source")
	}
	sources := make([]workspace.Source, 0, fs.NArg())
	for _, specification := range fs.Args() {
		alias, graphDir, ok := strings.Cut(specification, "=")
		if !ok || strings.TrimSpace(alias) == "" || strings.TrimSpace(graphDir) == "" {
			return fmt.Errorf("invalid merge source %q: expected alias=graph-directory", specification)
		}
		absolute, err := filepath.Abs(graphDir)
		if err != nil {
			return err
		}
		g, err := store.LoadGraph(absolute)
		if err != nil {
			return fmt.Errorf("load project %q graph: %w", alias, err)
		}
		sources = append(sources, workspace.Source{Alias: alias, Location: absolute, Graph: g})
	}
	merged, err := workspace.Merge(sources)
	if err != nil {
		return err
	}
	if err := writeWorkspaceGraph(*outDir, merged); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Merged %d project graphs into %s\n", len(sources), *outDir)
	return nil
}

func runGlobal(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("global requires add, remove, list, path, build, query, or context")
	}
	defaultPath, err := defaultRegistryPath()
	if err != nil {
		return err
	}
	subcommand := args[0]
	args = args[1:]
	switch subcommand {
	case "add":
		fs := newFlagSet("global add")
		registryPath := fs.String("registry", defaultPath, "registry path")
		if err := fs.Parse(flexibleFlags(args, "registry")); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return errors.New("global add requires an alias and graph directory")
		}
		registry, err := workspace.LoadRegistry(*registryPath)
		if err != nil {
			return err
		}
		registry, err = workspace.Register(registry, fs.Arg(0), fs.Arg(1))
		if err != nil {
			return err
		}
		if err := workspace.SaveRegistry(*registryPath, registry); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Registered %s in %s\n", fs.Arg(0), *registryPath)
		return nil
	case "remove":
		fs := newFlagSet("global remove")
		registryPath := fs.String("registry", defaultPath, "registry path")
		if err := fs.Parse(flexibleFlags(args, "registry")); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("global remove requires one alias")
		}
		registry, err := workspace.LoadRegistry(*registryPath)
		if err != nil {
			return err
		}
		registry, removed := workspace.Remove(registry, fs.Arg(0))
		if !removed {
			return fmt.Errorf("project alias %q is not registered", fs.Arg(0))
		}
		if err := workspace.SaveRegistry(*registryPath, registry); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed %s from %s\n", fs.Arg(0), *registryPath)
		return nil
	case "list":
		fs := newFlagSet("global list")
		registryPath := fs.String("registry", defaultPath, "registry path")
		if err := fs.Parse(flexibleFlags(args, "registry")); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("global list does not accept positional arguments")
		}
		registry, err := workspace.LoadRegistry(*registryPath)
		if err != nil {
			return err
		}
		if len(registry.Projects) == 0 {
			fmt.Fprintln(stdout, "No projects registered.")
			return nil
		}
		for _, project := range registry.Projects {
			fmt.Fprintf(stdout, "%s\t%s\n", project.Alias, project.GraphDir)
		}
		return nil
	case "path":
		fs := newFlagSet("global path")
		registryPath := fs.String("registry", defaultPath, "registry path")
		if err := fs.Parse(flexibleFlags(args, "registry")); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("global path does not accept positional arguments")
		}
		fmt.Fprintln(stdout, *registryPath)
		return nil
	case "build":
		fs := newFlagSet("global build")
		registryPath := fs.String("registry", defaultPath, "registry path")
		outDir := fs.String("out", ".ravel-global", "merged graph directory")
		if err := fs.Parse(flexibleFlags(args, "registry", "out")); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("global build does not accept positional arguments")
		}
		merged, projects, err := loadGlobalGraph(*registryPath)
		if err != nil {
			return err
		}
		if err := writeWorkspaceGraph(*outDir, merged); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Built %d-project global graph in %s\n", projects, *outDir)
		return nil
	case "query":
		fs := newFlagSet("global query")
		registryPath := fs.String("registry", defaultPath, "registry path")
		jsonOut := fs.Bool("json", false, "write JSON")
		limit := fs.Int("limit", 25, "max results")
		if err := fs.Parse(flexibleFlags(args, "registry", "limit")); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return errors.New("global query requires search text")
		}
		merged, _, err := loadGlobalGraph(*registryPath)
		if err != nil {
			return err
		}
		results := query.Search(merged, strings.Join(fs.Args(), " "), *limit)
		return query.WriteSearch(stdout, results, *jsonOut)
	case "context":
		defaults := config.Default()
		fs := newFlagSet("global context")
		registryPath := fs.String("registry", defaultPath, "registry path")
		jsonOut := fs.Bool("json", false, "write JSON")
		tokenBudget := fs.Int("token-budget", defaults.Retrieval.TokenBudget, "approximate output-token budget")
		maxDepth := fs.Int("max-depth", defaults.Retrieval.MaxDepth, "graph traversal depth")
		maxNodes := fs.Int("max-nodes", defaults.Retrieval.MaxNodes, "hard node limit")
		if err := fs.Parse(flexibleFlags(args, "registry", "token-budget", "max-depth", "max-nodes")); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return errors.New("global context requires a natural-language question")
		}
		merged, _, err := loadGlobalGraph(*registryPath)
		if err != nil {
			return err
		}
		result, err := query.NewIndex(merged).Retrieve(strings.Join(fs.Args(), " "), query.RetrieveOptions{
			Traversal: query.Traversal(defaults.Retrieval.Traversal), Direction: query.Direction(defaults.Retrieval.Direction),
			SeedLimit: defaults.Retrieval.SeedLimit, MaxDepth: *maxDepth, MaxNodes: *maxNodes,
			BranchFanout: defaults.Retrieval.BranchFanout, HubDegreeThreshold: defaults.Retrieval.HubDegreeThreshold, TokenBudget: *tokenBudget,
		})
		if err != nil {
			return err
		}
		return query.WriteRetrieval(stdout, result, *jsonOut)
	default:
		return fmt.Errorf("unknown global command %q", subcommand)
	}
}

func defaultRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".ravel", "registry.json"), nil
}

func loadGlobalGraph(registryPath string) (graph.Graph, int, error) {
	registry, err := workspace.LoadRegistry(registryPath)
	if err != nil {
		return graph.Graph{}, 0, err
	}
	merged, err := workspace.MergeRegistry(registry)
	return merged, len(registry.Projects), err
}

func writeWorkspaceGraph(outDir string, merged graph.Graph) error {
	prepared := community.Assign(merged)
	markdown := report.MarkdownPrepared(prepared, true)
	return store.RewriteGraphViews(outDir, prepared, markdown)
}

func runPRs(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("prs")
	outDir := fs.String("out", ".reporavel", "graph directory")
	manifest := fs.String("manifest", "", "offline GitHub PR JSON file")
	repository := fs.String("repo", "", "GitHub repository in owner/name form")
	base := fs.String("base", "", "base branch filter")
	limit := fs.Int("limit", 50, "maximum open pull requests")
	jsonOut := fs.Bool("json", false, "write JSON")
	conflictsOnly := fs.Bool("conflicts", false, "show graph-overlap conflicts only")
	valueFlags := []string{"out", "manifest", "repo", "base", "limit"}
	if err := fs.Parse(flexibleFlags(args, valueFlags...)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("prs accepts at most one pull-request number")
	}
	if *limit < 1 || *limit > 200 {
		return errors.New("prs limit must be between 1 and 200")
	}
	var number int
	if fs.NArg() == 1 {
		parsed, err := strconv.Atoi(fs.Arg(0))
		if err != nil || parsed < 1 {
			return fmt.Errorf("invalid pull-request number %q", fs.Arg(0))
		}
		number = parsed
	}
	pullRequests, err := loadPullRequests(ctx, *manifest, *repository, *base, *limit, number)
	if err != nil {
		return err
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	analysis := pranalysis.Analyze(g, pullRequests)
	if *jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(analysis)
	}
	return pranalysis.WriteText(stdout, analysis, *conflictsOnly)
}

func loadPullRequests(ctx context.Context, manifest, repository, base string, limit, number int) ([]pranalysis.PullRequest, error) {
	var data []byte
	singleObject := manifest == "" && number > 0
	if manifest != "" {
		payload, err := os.ReadFile(manifest)
		if err != nil {
			return nil, err
		}
		if len(payload) > 32<<20 {
			return nil, errors.New("PR manifest exceeds 32 MiB")
		}
		data = payload
	} else {
		fields := "number,title,url,headRefName,baseRefName,isDraft,reviewDecision,files,statusCheckRollup"
		ghArgs := []string{"pr"}
		if number > 0 {
			ghArgs = append(ghArgs, "view", strconv.Itoa(number), "--json", fields)
		} else {
			ghArgs = append(ghArgs, "list", "--state", "open", "--limit", strconv.Itoa(limit), "--json", fields)
			if base != "" {
				ghArgs = append(ghArgs, "--base", base)
			}
		}
		if repository != "" {
			ghArgs = append(ghArgs, "--repo", repository)
		}
		command := exec.CommandContext(ctx, "gh", ghArgs...)
		var output, commandError bytes.Buffer
		command.Stdout = &output
		command.Stderr = &commandError
		if err := command.Run(); err != nil {
			detail := strings.TrimSpace(commandError.String())
			if detail == "" {
				detail = err.Error()
			}
			return nil, fmt.Errorf("load GitHub pull requests: %s", detail)
		}
		data = output.Bytes()
	}
	if singleObject {
		var pullRequest pranalysis.PullRequest
		if err := json.Unmarshal(data, &pullRequest); err != nil {
			return nil, fmt.Errorf("parse pull-request JSON: %w", err)
		}
		return []pranalysis.PullRequest{pullRequest}, nil
	}
	var pullRequests []pranalysis.PullRequest
	if err := json.Unmarshal(data, &pullRequests); err != nil {
		return nil, fmt.Errorf("parse pull-request JSON: %w", err)
	}
	if len(pullRequests) > 200 {
		return nil, errors.New("PR input contains more than 200 pull requests")
	}
	if number > 0 {
		for _, pullRequest := range pullRequests {
			if pullRequest.Number == number {
				return []pranalysis.PullRequest{pullRequest}, nil
			}
		}
		return nil, fmt.Errorf("pull request #%d is not present in the manifest", number)
	}
	return pullRequests, nil
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

func runContext(args []string, stdout io.Writer) error {
	configPath := flagValue(args, "config", ".reporavel.yaml")
	cfg, err := loadCommandConfig(args, configPath)
	if err != nil {
		return err
	}
	fs := newFlagSet("context")
	configFlag := fs.String("config", configPath, "configuration file")
	outDir := fs.String("out", cfg.Output.Dir, "graph directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	traversal := fs.String("traversal", cfg.Retrieval.Traversal, "bfs or dfs")
	direction := fs.String("direction", cfg.Retrieval.Direction, "out, in, or both")
	relations := fs.String("relations", cfg.Retrieval.Relations, "comma-separated edge kinds")
	inferRelations := fs.Bool("infer-relations", cfg.Retrieval.InferRelations, "infer relation filters from the question")
	seedLimit := fs.Int("seed-limit", cfg.Retrieval.SeedLimit, "maximum lexical seeds")
	maxDepth := fs.Int("max-depth", cfg.Retrieval.MaxDepth, "graph traversal depth")
	maxNodes := fs.Int("max-nodes", cfg.Retrieval.MaxNodes, "hard node limit")
	branchFanout := fs.Int("branch-fanout", cfg.Retrieval.BranchFanout, "0 for automatic, positive for neighbors expanded per node")
	hubThreshold := fs.Int("hub-degree-threshold", cfg.Retrieval.HubDegreeThreshold, "0 for automatic, -1 to disable")
	tokenBudget := fs.Int("token-budget", cfg.Retrieval.TokenBudget, "approximate output-token budget")
	communityBoost := fs.Bool("community-boost", cfg.Retrieval.CommunityBoost, "prioritize neighbors in the same detected community")
	candidateShortlist := fs.Bool("candidate-shortlist", false, "favor a compact ranked candidate list over explanatory edges")
	valueFlags := []string{"config", "out", "traversal", "direction", "relations", "seed-limit", "max-depth", "max-nodes", "branch-fanout", "hub-degree-threshold", "token-budget"}
	if err := fs.Parse(flexibleFlags(args, valueFlags...)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("context requires a natural-language question")
	}
	if *configFlag != configPath {
		return errors.New("internal config flag parsing mismatch")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	var edgeKinds []graph.EdgeKind
	for _, value := range strings.Split(*relations, ",") {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "all") {
			edgeKinds = append(edgeKinds, graph.EdgeKind(value))
		}
	}
	result, err := query.NewIndex(g).Retrieve(strings.Join(fs.Args(), " "), query.RetrieveOptions{
		Traversal: query.Traversal(strings.ToLower(*traversal)), Direction: query.Direction(strings.ToLower(*direction)),
		Relations: edgeKinds, DisableRelationInference: !*inferRelations, SeedLimit: *seedLimit,
		MaxDepth: *maxDepth, MaxNodes: *maxNodes, BranchFanout: *branchFanout, HubDegreeThreshold: *hubThreshold, TokenBudget: *tokenBudget,
		CommunityBoost:     *communityBoost,
		CandidateShortlist: *candidateShortlist,
	})
	if err != nil {
		return err
	}
	return query.WriteRetrieval(stdout, result, *jsonOut)
}

func runAffected(args []string, stdout io.Writer) error {
	fs := newFlagSet("affected")
	outDir := fs.String("out", ".reporavel", "graph directory")
	jsonOut := fs.Bool("json", false, "write JSON")
	relations := fs.String("relations", "", "comma-separated edge kinds")
	maxDepth := fs.Int("max-depth", 2, "impact traversal depth")
	maxNodes := fs.Int("max-nodes", 100, "hard node limit")
	branchFanout := fs.Int("branch-fanout", 0, "0 for automatic, positive for neighbors expanded per node")
	hubThreshold := fs.Int("hub-degree-threshold", 0, "0 for automatic, -1 to disable")
	tokenBudget := fs.Int("token-budget", 2000, "approximate output-token budget")
	valueFlags := []string{"out", "relations", "max-depth", "max-nodes", "branch-fanout", "hub-degree-threshold", "token-budget"}
	if err := fs.Parse(flexibleFlags(args, valueFlags...)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("affected requires a file, symbol, or node id")
	}
	g, err := store.LoadGraph(*outDir)
	if err != nil {
		return err
	}
	var edgeKinds []graph.EdgeKind
	for _, value := range strings.Split(*relations, ",") {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "all") {
			edgeKinds = append(edgeKinds, graph.EdgeKind(value))
		}
	}
	result, err := query.NewIndex(g).Affected(strings.Join(fs.Args(), " "), query.RetrieveOptions{
		Relations: edgeKinds, MaxDepth: *maxDepth, MaxNodes: *maxNodes, BranchFanout: *branchFanout,
		HubDegreeThreshold: *hubThreshold, TokenBudget: *tokenBudget,
	})
	if err != nil {
		return err
	}
	return query.WriteAffected(stdout, result, *jsonOut)
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
	ex, err := query.NewIndex(g).ExplainResolved(strings.Join(fs.Args(), " "))
	if err != nil {
		return err
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
	result, ok, err := query.NewIndex(g).ShortestPathResult(fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(stdout, "No path found.")
		return nil
	}
	return query.WritePathResult(stdout, result, *jsonOut)
}

func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := newFlagSet("mcp")
	outDir := fs.String("out", ".reporavel", "graph directory")
	transport := fs.String("transport", "stdio", "transport: stdio or http")
	address := fs.String("address", "127.0.0.1:8080", "HTTP listen address")
	path := fs.String("path", "/mcp", "HTTP endpoint path")
	apiKeyEnv := fs.String("api-key-env", "RAVEL_MCP_API_KEY", "environment variable containing the HTTP API key")
	maxMessageBytes := fs.Int("max-message-bytes", 0, "maximum MCP request size in bytes")
	if err := fs.Parse(flexibleFlags(args, "out", "transport", "address", "path", "api-key-env", "max-message-bytes")); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("mcp does not accept positional arguments")
	}
	switch strings.ToLower(strings.TrimSpace(*transport)) {
	case "stdio":
		return mcp.Serve(ctx, stdin, stdout, mcp.Options{OutDir: *outDir, Version: Version, MaxMessageBytes: *maxMessageBytes})
	case "http":
		environmentName := strings.TrimSpace(*apiKeyEnv)
		if environmentName == "" {
			return errors.New("mcp --api-key-env must not be empty")
		}
		return mcp.ServeStreamableHTTP(ctx, mcp.HTTPOptions{
			OutDir: *outDir, Version: Version, Address: *address, Path: *path,
			APIKey: os.Getenv(environmentName), MaxMessageBytes: *maxMessageBytes,
		})
	default:
		return fmt.Errorf("unsupported MCP transport %q (want stdio or http)", *transport)
	}
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
	configPath := flagValue(args, "config", ".reporavel.yaml")
	cfg, err := loadCommandConfig(args, configPath)
	if err != nil {
		return err
	}
	fs := newFlagSet("benchmark")
	configFlag := fs.String("config", configPath, "configuration file")
	graphDir := fs.String("graph", cfg.Output.Dir, "graph directory")
	dataset := fs.String("dataset", "", "evaluation JSONL file")
	answers := fs.String("answers", "", "external answer-quality JSONL ledger")
	gatePath := fs.String("gate", "", "retrieval quality gate JSON file")
	out := fs.String("out", "-", "result JSON file or - for stdout")
	topK := fs.Int("top-k", cfg.Retrieval.MaxNodes, "retrieval result count and context-node cap")
	retriever := fs.String("retriever", "context", "context or flat")
	traversal := fs.String("traversal", cfg.Retrieval.Traversal, "bfs or dfs")
	direction := fs.String("direction", cfg.Retrieval.Direction, "out, in, or both")
	relations := fs.String("relations", cfg.Retrieval.Relations, "comma-separated edge kinds")
	inferRelations := fs.Bool("infer-relations", cfg.Retrieval.InferRelations, "infer relation filters from each question")
	seedLimit := fs.Int("seed-limit", cfg.Retrieval.SeedLimit, "maximum lexical seeds")
	maxDepth := fs.Int("max-depth", cfg.Retrieval.MaxDepth, "graph traversal depth")
	branchFanout := fs.Int("branch-fanout", cfg.Retrieval.BranchFanout, "0 for automatic, positive for neighbors expanded per node")
	hubThreshold := fs.Int("hub-degree-threshold", cfg.Retrieval.HubDegreeThreshold, "0 for automatic, -1 to disable")
	tokenBudget := fs.Int("token-budget", cfg.Retrieval.TokenBudget, "approximate output-token budget")
	communityBoost := fs.Bool("community-boost", cfg.Retrieval.CommunityBoost, "prioritize neighbors in the same detected community")
	datasetRevision := fs.String("dataset-revision", "unspecified", "dataset revision or commit")
	graphRevision := fs.String("graph-revision", "unspecified", "source revision used to build the graph")
	adapterVersion := fs.String("adapter-version", "graph-query-jsonl-v2", "dataset adapter version")
	valueFlags := []string{"config", "graph", "dataset", "answers", "gate", "out", "top-k", "retriever", "traversal", "direction", "relations", "seed-limit", "max-depth", "branch-fanout", "hub-degree-threshold", "token-budget", "dataset-revision", "graph-revision", "adapter-version"}
	if err := fs.Parse(flexibleFlags(args, valueFlags...)); err != nil {
		return err
	}
	if fs.NArg() != 0 || *dataset == "" {
		return errors.New("benchmark requires --dataset <cases.jsonl>")
	}
	if *configFlag != configPath {
		return errors.New("internal config flag parsing mismatch")
	}
	g, err := store.LoadGraph(*graphDir)
	if err != nil {
		return err
	}
	cases, datasetHash, err := evaluation.LoadJSONLWithHash(*dataset)
	if err != nil {
		return err
	}
	var qualityGate *evaluation.QualityGate
	var qualityGateHash string
	if *gatePath != "" {
		gate, hash, err := evaluation.LoadQualityGateWithHash(*gatePath)
		if err != nil {
			return err
		}
		qualityGate = &gate
		qualityGateHash = hash
		if gate.RequireFreshExpectations {
			if err := evaluation.ValidateExpectedIDs(g, cases); err != nil {
				return err
			}
		}
	}
	graphHash, err := evaluation.GraphHash(g)
	if err != nil {
		return err
	}
	var edgeKinds []graph.EdgeKind
	for _, value := range strings.Split(*relations, ",") {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "all") {
			edgeKinds = append(edgeKinds, graph.EdgeKind(value))
		}
	}
	result, err := evaluation.RunWithOptions(g, cases, evaluation.RunOptions{
		Retriever: strings.ToLower(*retriever), TopK: *topK,
		Retrieval: query.RetrieveOptions{
			Traversal: query.Traversal(strings.ToLower(*traversal)), Direction: query.Direction(strings.ToLower(*direction)),
			Relations: edgeKinds, SeedLimit: *seedLimit, MaxDepth: *maxDepth, MaxNodes: *topK, BranchFanout: *branchFanout,
			DisableRelationInference: !*inferRelations, HubDegreeThreshold: *hubThreshold, TokenBudget: *tokenBudget,
			CommunityBoost: *communityBoost,
		},
		RavelVersion: Version, DatasetRevision: *datasetRevision, DatasetSHA256: datasetHash, AdapterVersion: *adapterVersion,
		GraphSHA256: graphHash, GraphRevision: *graphRevision,
	})
	if err != nil {
		return err
	}
	if err := attachAnswerLedger(&result, cases, *answers); err != nil {
		return err
	}
	var gateErr error
	if qualityGate != nil {
		gateResult := evaluation.EvaluateQualityGate(result, *qualityGate, qualityGateHash)
		result.QualityGate = &gateResult
		gateErr = gateResult.Error()
	}
	if *out == "-" {
		if err := evaluation.Write(stdout, result); err != nil {
			return err
		}
		return gateErr
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
	return gateErr
}

func loadCommandConfig(args []string, path string) (config.Config, error) {
	if flagProvided(args, "config") {
		return config.LoadRequired(path)
	}
	return config.Load(path)
}

func communityOptions(output config.OutputConfig) community.Options {
	return community.Options{Granularity: community.Preset(output.CommunityGranularity), HubDegreeThreshold: output.CommunityHubDegreeThreshold}
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

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(item *flag.Flag) {
		if item.Name == name {
			found = true
		}
	})
	return found
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

func flagValue(args []string, name, fallback string) string {
	long := "--" + name
	short := "-" + name
	for i, arg := range args {
		if arg == long || arg == short {
			if i+1 < len(args) {
				return args[i+1]
			}
			return fallback
		}
		if key, value, ok := strings.Cut(arg, "="); ok && (key == long || key == short) {
			return value
		}
	}
	return fallback
}

func flagProvided(args []string, name string) bool {
	long := "--" + name
	short := "-" + name
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == long || arg == short {
			return true
		}
		if key, _, ok := strings.Cut(arg, "="); ok && (key == long || key == short) {
			return true
		}
	}
	return false
}

func commandHelpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func commandUsage(w io.Writer, command string) error {
	lines := map[string]string{
		"version":       "ravel version",
		"self-update":   "ravel self-update [--version latest] [--platforms codex,claude] [--project]",
		"update-check":  "ravel update-check [--json] [--repo 12vault/ravel]",
		"init":          "ravel init",
		"install":       "ravel install [--platform <name>] [--project]",
		"uninstall":     "ravel uninstall [--platform <name>] [--project]",
		"hook":          "ravel hook <install|uninstall|status> [root]",
		"ingest":        "ravel ingest [--out <dir>] <fragment.json>",
		"community":     "ravel community [--json|--template] [--granularity coarse|balanced|fine] | ravel community describe <file>",
		"dashboard":     "ravel dashboard [--out <dir>] [--communities=false] [--community-granularity <preset>] [--community-hub-degree-threshold <n>]",
		"doctor":        "ravel doctor",
		"tools":         "ravel tools",
		"extract":       "ravel extract [--json] <audited-doc-or-pdf>...",
		"plan":          "ravel plan [--json] <route> [paths...]",
		"audit":         "ravel audit [--config <path>] [--out <dir>] [root]",
		"scan":          "ravel scan [--config <path>] [--out <dir>] [root]",
		"build":         "ravel build [--config <path>] [--out <dir>] [--jobs <n>] [root]",
		"update":        "ravel update [--config <path>] [--out <dir>] [--jobs <n>] [root]",
		"watch":         "ravel watch [--interval 2s] [root]",
		"share":         "ravel share [--from <dir>] [--out ravel-graph]",
		"merge":         "ravel merge [--out <dir>] <alias=graph-directory>...",
		"global":        "ravel global <add|remove|list|path|build|query|context> [options]",
		"prs":           "ravel prs [--out <dir>] [--repo owner/name] [--base branch] [--conflicts] [--json] [number]",
		"report":        "ravel report [--out <dir>]",
		"query":         "ravel query [--out <dir>] [--limit 25] [--json] <text>",
		"context-batch": "ravel context-batch [options]",
		"affected":      "ravel affected [--out <dir>] [--json] [--max-depth 2] [--branch-fanout 0] [--relations <kinds>] <file-or-symbol>",
		"explain":       "ravel explain [--out <dir>] [--json] <file-or-symbol>",
		"path":          "ravel path [--out <dir>] [--json] <from> <to>",
		"mcp":           "ravel mcp [--out <graphdir>] [--transport stdio|http] [--address 127.0.0.1:8080] [--path /mcp] [--api-key-env RAVEL_MCP_API_KEY]",
		"tech":          "ravel tech [--out <dir>] [--json]",
		"understand":    "ravel understand [--out <dir>] [--json]",
		"learn":         "ravel learn [--out <dir>] [--json]",
		"docs":          "ravel docs [--out <dir>] [--json]",
		"pdf":           "ravel pdf [--out <dir>] [--json]",
		"schema":        "ravel schema [--out <dir>] [--json]",
		"diff":          "ravel diff [--out <dir>] [--json] [changed-path]...",
	}
	switch command {
	case "context":
		fmt.Fprintln(w, "Usage: ravel context [options] <question>")
		fmt.Fprintln(w, "Options: --config --out --json --traversal --direction --relations --infer-relations --seed-limit --max-depth --max-nodes --branch-fanout --hub-degree-threshold --token-budget --community-boost --candidate-shortlist")
		return nil
	case "context-batch":
		fmt.Fprintln(w, "Usage: ravel context-batch [options]")
		fmt.Fprintln(w, "Reads JSONL {\"id\":\"...\",\"question\":\"...\"} requests from stdin and writes JSONL responses.")
		fmt.Fprintln(w, "Options: --config --out --traversal --direction --relations --infer-relations --seed-limit --max-depth --max-nodes --branch-fanout --hub-degree-threshold --token-budget --community-boost --candidate-shortlist")
		return nil
	case "benchmark":
		fmt.Fprintln(w, "Usage: ravel benchmark --dataset <cases.jsonl> [options]")
		fmt.Fprintln(w, "Options: --config --graph --answers --out --retriever --top-k --traversal --direction --relations --infer-relations --seed-limit --max-depth --branch-fanout --hub-degree-threshold --token-budget --community-boost --dataset-revision --graph-revision --adapter-version")
		return nil
	}
	line, ok := lines[command]
	if !ok {
		return fmt.Errorf("unknown command %q", command)
	}
	fmt.Fprintln(w, "Usage:", line)
	return nil
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "RepoRavel %s\n", Version)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  ravel version")
	fmt.Fprintln(w, "  ravel update-check [--json]")
	fmt.Fprintln(w, "  ravel self-update [--version latest] [--platforms codex,claude] [--project]")
	fmt.Fprintln(w, "  ravel init")
	fmt.Fprintln(w, "  ravel install [--platform <name>] [--project]")
	fmt.Fprintln(w, "  ravel uninstall [--platform <name>] [--project]")
	fmt.Fprintln(w, "  ravel codex|claude|cursor|vscode|gemini|opencode <install|uninstall>")
	fmt.Fprintln(w, "  ravel hook <install|uninstall|status> [root]")
	fmt.Fprintln(w, "  ravel ingest [--out <dir>] <fragment.json>")
	fmt.Fprintln(w, "  ravel community [--json|--template] [--granularity coarse|balanced|fine]")
	fmt.Fprintln(w, "  ravel community describe <descriptions.json>")
	fmt.Fprintln(w, "  ravel dashboard [--out <dir>] [--communities=false] [--community-granularity balanced]")
	fmt.Fprintln(w, "  ravel doctor")
	fmt.Fprintln(w, "  ravel tools")
	fmt.Fprintln(w, "  ravel extract [--json] <audited-doc-or-pdf>...")
	fmt.Fprintln(w, "  ravel plan [--json] <route> [paths...]")
	fmt.Fprintln(w, "  ravel benchmark --dataset <cases.jsonl> [--answers <judgments.jsonl>] [--gate <quality-gate.json>] [--retriever context|flat] [--token-budget 2000]")
	fmt.Fprintln(w, "  ravel audit [root]")
	fmt.Fprintln(w, "  ravel build [--jobs 4] [root]")
	fmt.Fprintln(w, "  ravel update [--jobs 4] [root]")
	fmt.Fprintln(w, "  ravel watch [--interval 2s] [root]")
	fmt.Fprintln(w, "  ravel share [--out ravel-graph]")
	fmt.Fprintln(w, "  ravel merge [--out .ravel-workspace] <alias=graph-directory>...")
	fmt.Fprintln(w, "  ravel global <add|remove|list|path|build|query|context> [options]")
	fmt.Fprintln(w, "  ravel prs [--repo owner/name] [--base branch] [--conflicts] [--json] [number]")
	fmt.Fprintln(w, "  ravel report")
	fmt.Fprintln(w, "  ravel query [--json] <text>")
	fmt.Fprintln(w, "  ravel context [--json] [--token-budget 2000] [--max-depth 2] [--branch-fanout 0] <question>")
	fmt.Fprintln(w, "  ravel context-batch [--out <dir>] [--token-budget 2000]")
	fmt.Fprintln(w, "  ravel affected [--json] [--max-depth 2] [--branch-fanout 0] [--relations calls,references] <file-or-symbol>")
	fmt.Fprintln(w, "  ravel explain [--json] <file-or-symbol>")
	fmt.Fprintln(w, "  ravel path [--json] <from> <to>")
	fmt.Fprintln(w, "  ravel mcp [--out <graphdir>] [--transport stdio|http]")
	fmt.Fprintln(w, "  ravel tech|understand|learn|docs|pdf|schema [--json]")
	fmt.Fprintln(w, "  ravel diff [--json] <changed-path>...")
}
