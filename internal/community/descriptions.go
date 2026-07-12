package community

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/12vault/ravel/internal/graph"
)

const (
	maximumDescriptionBytes = 2_000
	maximumRationaleBytes   = 2_000
	maximumSourceBytes      = 200
)

type DescriptionFile struct {
	Version      int           `json:"version"`
	Source       string        `json:"source"`
	Descriptions []Description `json:"descriptions"`
}

type Description struct {
	Community string `json:"community"`
	Text      string `json:"description"`
	Rationale string `json:"rationale"`
}

func LoadDescriptions(path string) (DescriptionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DescriptionFile{}, err
	}
	var file DescriptionFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return DescriptionFile{}, fmt.Errorf("parse community descriptions: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return DescriptionFile{}, errors.New("parse community descriptions: trailing JSON content")
	}
	if err := ValidateDescriptions(file); err != nil {
		return DescriptionFile{}, err
	}
	return file, nil
}

func ValidateDescriptions(file DescriptionFile) error {
	if file.Version != 1 {
		return fmt.Errorf("unsupported community description version %d", file.Version)
	}
	source := strings.TrimSpace(file.Source)
	if source == "" {
		return errors.New("community description source is required")
	}
	if !utf8.ValidString(source) || len(source) > maximumSourceBytes {
		return fmt.Errorf("community description source must be valid UTF-8 and at most %d bytes", maximumSourceBytes)
	}
	if len(file.Descriptions) == 0 {
		return errors.New("at least one community description is required")
	}
	seen := map[string]bool{}
	for i, item := range file.Descriptions {
		item.Community = strings.TrimSpace(item.Community)
		item.Text = strings.TrimSpace(item.Text)
		item.Rationale = strings.TrimSpace(item.Rationale)
		if item.Community == "" {
			return fmt.Errorf("descriptions[%d].community is required", i)
		}
		if seen[item.Community] {
			return fmt.Errorf("duplicate community description %q", item.Community)
		}
		seen[item.Community] = true
		if item.Text == "" {
			return fmt.Errorf("descriptions[%d].description is required", i)
		}
		if !utf8.ValidString(item.Text) || len(item.Text) > maximumDescriptionBytes {
			return fmt.Errorf("descriptions[%d].description must be valid UTF-8 and at most %d bytes", i, maximumDescriptionBytes)
		}
		if item.Rationale == "" {
			return fmt.Errorf("descriptions[%d].rationale is required for inferred AI content", i)
		}
		if !utf8.ValidString(item.Rationale) || len(item.Rationale) > maximumRationaleBytes {
			return fmt.Errorf("descriptions[%d].rationale must be valid UTF-8 and at most %d bytes", i, maximumRationaleBytes)
		}
	}
	return nil
}

// ApplyDescriptions attaches optional inferred descriptions without changing
// community membership, stable IDs, or deterministic names.
func ApplyDescriptions(g graph.Graph, file DescriptionFile) (graph.Graph, error) {
	if err := ValidateDescriptions(file); err != nil {
		return graph.Graph{}, err
	}
	available := map[string]bool{}
	for _, node := range g.Nodes {
		if id := node.Meta[MetaKey]; id != "" {
			available[id] = true
		}
	}
	if len(available) == 0 {
		return graph.Graph{}, errors.New("graph has no assigned communities")
	}
	byID := map[string]Description{}
	for _, item := range file.Descriptions {
		item.Community = strings.TrimSpace(item.Community)
		if !available[item.Community] {
			return graph.Graph{}, fmt.Errorf("community %q is not present in the graph", item.Community)
		}
		byID[item.Community] = item
	}
	out := g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		item, ok := byID[out.Nodes[i].Meta[MetaKey]]
		if !ok {
			continue
		}
		meta := make(map[string]string, len(out.Nodes[i].Meta)+4)
		for key, value := range out.Nodes[i].Meta {
			meta[key] = value
		}
		meta[MetaDescriptionKey] = strings.TrimSpace(item.Text)
		meta[MetaDescriptionSourceKey] = strings.TrimSpace(file.Source)
		meta[MetaDescriptionConfidenceKey] = "inferred"
		meta[MetaDescriptionRationaleKey] = strings.TrimSpace(item.Rationale)
		out.Nodes[i].Meta = meta
	}
	return out, nil
}
