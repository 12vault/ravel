package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
)

type Fragment struct {
	Version     int                `json:"version"`
	Source      string             `json:"source"`
	SourcePaths []string           `json:"sourcePaths"`
	Nodes       []graph.Node       `json:"nodes"`
	Edges       []graph.Edge       `json:"edges"`
	Diagnostics []graph.Diagnostic `json:"diagnostics,omitempty"`
}

func Load(path string) (Fragment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fragment{}, err
	}
	var fragment Fragment
	if err := json.Unmarshal(data, &fragment); err != nil {
		return Fragment{}, fmt.Errorf("parse fragment: %w", err)
	}
	if err := Validate(fragment); err != nil {
		return Fragment{}, err
	}
	return fragment, nil
}

func Validate(fragment Fragment) error {
	if fragment.Version != 1 {
		return fmt.Errorf("unsupported fragment version %d", fragment.Version)
	}
	if strings.TrimSpace(fragment.Source) == "" {
		return errors.New("fragment source is required")
	}
	if len(fragment.Nodes)+len(fragment.Edges) > 0 && len(fragment.SourcePaths) == 0 {
		return errors.New("fragment sourcePaths are required")
	}
	for i, path := range fragment.SourcePaths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("sourcePaths[%d] is empty", i)
		}
	}
	seen := map[string]bool{}
	for i, node := range fragment.Nodes {
		if node.ID == "" || node.Kind == "" || node.Name == "" {
			return fmt.Errorf("node %d requires id, kind, and name", i)
		}
		if seen[node.ID] {
			return fmt.Errorf("duplicate node id %q", node.ID)
		}
		if err := validateProvenance(node.Meta, fmt.Sprintf("node %d", i)); err != nil {
			return err
		}
		seen[node.ID] = true
	}
	for i, edge := range fragment.Edges {
		if edge.Kind == "" || edge.From == "" || edge.To == "" {
			return fmt.Errorf("edge %d requires kind, from, and to", i)
		}
		if err := validateProvenance(edge.Meta, fmt.Sprintf("edge %d", i)); err != nil {
			return err
		}
	}
	return nil
}

func Apply(current graph.Graph, fragment Fragment) (graph.Graph, error) {
	if err := Validate(fragment); err != nil {
		return graph.Graph{}, err
	}
	builder := graph.NewBuilder(current.Root)
	known := map[string]bool{}
	sourceHashes, err := evidenceHashes(current, fragment.SourcePaths)
	if err != nil {
		return graph.Graph{}, err
	}
	for _, node := range current.Nodes {
		known[node.ID] = true
		builder.AddNode(node)
	}
	for _, node := range fragment.Nodes {
		node.Meta = provenance(node.Meta, fragment.Source, sourceHashes)
		known[node.ID] = true
		builder.AddNode(node)
	}
	for _, edge := range current.Edges {
		if !known[edge.From] || !known[edge.To] {
			return graph.Graph{}, fmt.Errorf("edge %s references unknown endpoint %q -> %q", edge.Kind, edge.From, edge.To)
		}
		builder.AddEdge(edge)
	}
	for _, edge := range fragment.Edges {
		if !known[edge.From] || !known[edge.To] {
			return graph.Graph{}, fmt.Errorf("edge %s references unknown endpoint %q -> %q", edge.Kind, edge.From, edge.To)
		}
		edge.Meta = provenance(edge.Meta, fragment.Source, sourceHashes)
		builder.AddEdge(edge)
	}
	for _, diagnostic := range current.Diagnostics {
		builder.AddDiagnostic(diagnostic)
	}
	for _, diagnostic := range fragment.Diagnostics {
		builder.AddDiagnostic(diagnostic)
	}
	return builder.Build(), nil
}

func validateProvenance(meta map[string]string, label string) error {
	confidence := strings.TrimSpace(meta["confidence"])
	if confidence == "" {
		confidence = "inferred"
	}
	switch confidence {
	case "extracted":
		if strings.TrimSpace(meta["evidence"]) == "" {
			return fmt.Errorf("%s with extracted confidence requires meta.evidence", label)
		}
	case "inferred":
		if strings.TrimSpace(meta["rationale"]) == "" {
			return fmt.Errorf("%s with inferred confidence requires meta.rationale", label)
		}
	default:
		return fmt.Errorf("%s has invalid confidence %q", label, confidence)
	}
	return nil
}

func evidenceHashes(current graph.Graph, paths []string) (string, error) {
	available := map[string]string{}
	for _, node := range current.Nodes {
		if node.Kind == graph.NodeFile && node.Path != "" && node.Meta["hash"] != "" {
			available[node.Path] = node.Meta["hash"]
		}
	}
	hashes := map[string]string{}
	for _, path := range paths {
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		hash, ok := available[path]
		if !ok {
			return "", fmt.Errorf("source path %q is not in the current graph", path)
		}
		hashes[path] = hash
	}
	data, err := json.Marshal(hashes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func provenance(meta map[string]string, source, sourceHashes string) map[string]string {
	out := map[string]string{}
	for key, value := range meta {
		out[key] = value
	}
	if out["source"] == "" {
		out["source"] = source
	}
	if out["confidence"] == "" {
		out["confidence"] = "inferred"
	}
	out["sourceHashes"] = sourceHashes
	return out
}
