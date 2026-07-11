package orchestrate

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
)

type Task struct {
	ID             string   `json:"id"`
	Role           string   `json:"role"`
	DependsOn      []string `json:"dependsOn,omitempty"`
	SourcePaths    []string `json:"sourcePaths,omitempty"`
	ExpectedOutput string   `json:"expectedOutput"`
}

type Plan struct {
	Version int    `json:"version"`
	Route   string `json:"route"`
	Tasks   []Task `json:"tasks"`
}

func Build(route string, g graph.Graph, targets []string, batchSize int, outDir string) (Plan, error) {
	route = strings.ToLower(strings.TrimSpace(route))
	if batchSize < 1 {
		return Plan{}, fmt.Errorf("batch size must be positive")
	}
	files := filePaths(g, route, targets)
	plan := Plan{Version: 1, Route: route}
	add := func(id, role string, deps, paths []string) {
		plan.Tasks = append(plan.Tasks, Task{ID: id, Role: role, DependsOn: deps, SourcePaths: paths, ExpectedOutput: filepath.ToSlash(filepath.Join(outDir, "tasks", id+".json"))})
	}
	switch route {
	case "tech":
		add("scan", "project-scanner", nil, files)
		codeIDs := addBatches(&plan, "code", "code-analyzer", []string{"scan"}, codeFiles(g, files), batchSize, outDir)
		add("review", "graph-reviewer", append([]string{"scan"}, codeIDs...), files)
	case "understand":
		add("scan", "project-scanner", nil, files)
		codeIDs := addBatches(&plan, "code", "code-analyzer", []string{"scan"}, codeFiles(g, files), batchSize, outDir)
		architectureDeps := append([]string{"scan"}, codeIDs...)
		add("architecture", "architecture-analyzer", architectureDeps, files)
		add("domain", "domain-analyzer", []string{"architecture"}, files)
		add("review", "graph-reviewer", []string{"architecture", "domain"}, files)
	case "learn":
		add("tour", "tour-builder", nil, files)
		add("review", "graph-reviewer", []string{"tour"}, files)
	case "docs", "pdf", "schema":
		add("documents", "document-article-analyzer", nil, files)
		add("domain", "domain-analyzer", []string{"documents"}, files)
		add("review", "graph-reviewer", []string{"documents", "domain"}, files)
	case "diff":
		if len(targets) == 0 {
			return Plan{}, fmt.Errorf("diff plan requires changed paths")
		}
		add("architecture-impact", "architecture-analyzer", nil, files)
		add("domain-impact", "domain-analyzer", nil, files)
		add("review", "graph-reviewer", []string{"architecture-impact", "domain-impact"}, files)
	default:
		return Plan{}, fmt.Errorf("unsupported plan route %q", route)
	}
	return plan, nil
}

func Write(w io.Writer, plan Plan, jsonOutput bool) error {
	if jsonOutput {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
	fmt.Fprintf(w, "Ravel %s agent plan\n", plan.Route)
	for _, task := range plan.Tasks {
		fmt.Fprintf(w, "%s\t%s\t%d paths", task.ID, task.Role, len(task.SourcePaths))
		if len(task.DependsOn) > 0 {
			fmt.Fprintf(w, "\tafter %s", strings.Join(task.DependsOn, ","))
		}
		fmt.Fprintf(w, "\t-> %s\n", task.ExpectedOutput)
	}
	return nil
}

func addBatches(plan *Plan, prefix, role string, deps, files []string, size int, outDir string) []string {
	var ids []string
	for start, index := 0, 1; start < len(files); start, index = start+size, index+1 {
		end := start + size
		if end > len(files) {
			end = len(files)
		}
		id := fmt.Sprintf("%s-%03d", prefix, index)
		ids = append(ids, id)
		plan.Tasks = append(plan.Tasks, Task{ID: id, Role: role, DependsOn: deps, SourcePaths: files[start:end], ExpectedOutput: filepath.ToSlash(filepath.Join(outDir, "tasks", id+".json"))})
	}
	return ids
}

func filePaths(g graph.Graph, route string, targets []string) []string {
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[filepath.ToSlash(target)] = true
	}
	var paths []string
	for _, node := range g.Nodes {
		if node.Kind != graph.NodeFile || node.Path == "" {
			continue
		}
		language := node.Meta["language"]
		include := len(targetSet) == 0 || targetSet[node.Path]
		switch route {
		case "docs":
			include = language == "markdown" || language == "documentation" || language == "text"
		case "pdf":
			include = language == "pdf"
		case "schema":
			include = language == "sql" || language == "graphql" || language == "protobuf"
		}
		if include {
			paths = append(paths, node.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func codeFiles(g graph.Graph, allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, path := range allowed {
		allowedSet[path] = true
	}
	var paths []string
	for _, node := range g.Nodes {
		if node.Kind != graph.NodeFile || !allowedSet[node.Path] {
			continue
		}
		switch node.Meta["language"] {
		case "markdown", "documentation", "text", "pdf", "sql", "yaml", "json", "toml", "xml", "css":
			continue
		}
		paths = append(paths, node.Path)
	}
	sort.Strings(paths)
	return paths
}
