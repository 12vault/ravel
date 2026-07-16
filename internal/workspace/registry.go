package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/store"
)

const (
	RegistryVersion      = 1
	maxRegistryFileBytes = 1 << 20
)

type Registry struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

type Project struct {
	Alias    string `json:"alias"`
	GraphDir string `json:"graphDir"`
}

func EmptyRegistry() Registry {
	return Registry{Version: RegistryVersion}
}

func LoadRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return EmptyRegistry(), nil
	}
	if err != nil {
		return Registry{}, err
	}
	if len(data) > maxRegistryFileBytes {
		return Registry{}, fmt.Errorf("registry exceeds %d bytes", maxRegistryFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Registry{}, err
	}
	if err := ValidateRegistry(registry); err != nil {
		return Registry{}, err
	}
	sortProjects(registry.Projects)
	return registry, nil
}

func SaveRegistry(path string, registry Registry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	sortProjects(registry.Projects)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".registry.tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func ValidateRegistry(registry Registry) error {
	if registry.Version != RegistryVersion {
		return fmt.Errorf("unsupported registry version %d", registry.Version)
	}
	seen := map[string]bool{}
	for i, project := range registry.Projects {
		if !validAlias.MatchString(project.Alias) {
			return fmt.Errorf("project %d has invalid alias %q", i, project.Alias)
		}
		if seen[project.Alias] {
			return fmt.Errorf("duplicate project alias %q", project.Alias)
		}
		seen[project.Alias] = true
		if !filepath.IsAbs(project.GraphDir) {
			return fmt.Errorf("project %q graphDir must be absolute", project.Alias)
		}
	}
	return nil
}

func Register(registry Registry, alias, graphDir string) (Registry, error) {
	alias = strings.TrimSpace(alias)
	if !validAlias.MatchString(alias) {
		return Registry{}, fmt.Errorf("invalid project alias %q", alias)
	}
	absDir, err := filepath.Abs(graphDir)
	if err != nil {
		return Registry{}, err
	}
	if _, err := store.LoadGraph(absDir); err != nil {
		return Registry{}, fmt.Errorf("load project %q graph: %w", alias, err)
	}
	if registry.Version == 0 {
		registry.Version = RegistryVersion
	}
	replaced := false
	for i := range registry.Projects {
		if registry.Projects[i].Alias == alias {
			registry.Projects[i].GraphDir = absDir
			replaced = true
			break
		}
	}
	if !replaced {
		registry.Projects = append(registry.Projects, Project{Alias: alias, GraphDir: absDir})
	}
	sortProjects(registry.Projects)
	return registry, ValidateRegistry(registry)
}

func Remove(registry Registry, alias string) (Registry, bool) {
	projects := registry.Projects[:0]
	removed := false
	for _, project := range registry.Projects {
		if project.Alias == alias {
			removed = true
			continue
		}
		projects = append(projects, project)
	}
	registry.Projects = projects
	return registry, removed
}

func MergeRegistry(registry Registry) (graphResult graph.Graph, err error) {
	if err := ValidateRegistry(registry); err != nil {
		return graph.Graph{}, err
	}
	if len(registry.Projects) == 0 {
		return graph.Graph{}, errors.New("registry contains no projects")
	}
	sources := make([]Source, 0, len(registry.Projects))
	for _, project := range registry.Projects {
		g, err := store.LoadGraph(project.GraphDir)
		if err != nil {
			return graph.Graph{}, fmt.Errorf("load project %q graph: %w", project.Alias, err)
		}
		sources = append(sources, Source{Alias: project.Alias, Location: project.GraphDir, Graph: g})
	}
	return Merge(sources)
}

func sortProjects(projects []Project) {
	sort.Slice(projects, func(i, j int) bool { return projects[i].Alias < projects[j].Alias })
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("registry contains trailing JSON data")
	}
	return fmt.Errorf("parse registry trailing data: %w", err)
}
