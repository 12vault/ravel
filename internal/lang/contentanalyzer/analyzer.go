package contentanalyzer

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/lang"
	"github.com/12ya/reporavel/internal/scan"
)

var (
	markdownLink = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	createTable  = regexp.MustCompile(`(?i)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?["` + "`" + `\[]?([\w.]+)`)
	columnLine   = regexp.MustCompile(`^\s*["` + "`" + `\[]?([A-Za-z_][\w]*)["` + "`" + `\]]?\s+([A-Za-z][\w()]*)`)
)

type Analyzer struct {
	language string
}

func Markdown() *Analyzer { return &Analyzer{language: "markdown"} }
func SQL() *Analyzer      { return &Analyzer{language: "sql"} }

func (a *Analyzer) Language() string { return a.language }

func (a *Analyzer) Extensions() []string {
	if a.language == "sql" {
		return []string{".sql"}
	}
	return []string{".md", ".mdx"}
}

func (a *Analyzer) Analyze(ctx context.Context, _ string, files []scan.File) (*lang.AnalysisResult, error) {
	result := &lang.AnalysisResult{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(file.AbsPath)
		if err != nil {
			return nil, err
		}
		if a.language == "sql" {
			analyzeSQL(file, string(data), result)
		} else {
			analyzeMarkdown(file, string(data), result)
		}
	}
	return result, nil
}

func analyzeMarkdown(file scan.File, content string, result *lang.AnalysisResult) {
	documentID := graph.ContentID("document", file.Path)
	result.Nodes = append(result.Nodes, graph.Node{ID: documentID, Kind: graph.NodeDocument, Name: file.Path, Path: file.Path, Meta: map[string]string{"confidence": "extracted"}})
	result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(file.Path), To: documentID, Meta: map[string]string{"confidence": "extracted"}})

	scanner := bufio.NewScanner(strings.NewReader(content))
	line := 0
	inFence := false
	fenceMarker := ""
	for scanner.Scan() {
		line++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		if marker := markdownFence(trimmed); marker != "" {
			if !inFence {
				inFence, fenceMarker = true, marker
			} else if marker == fenceMarker {
				inFence, fenceMarker = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 6 || level == len(trimmed) || trimmed[level] != ' ' {
				continue
			}
			name := strings.TrimSpace(strings.TrimSuffix(trimmed[level:], "#"))
			if name != "" {
				sectionID := graph.ContentID("section", file.Path, lineString(line))
				result.Nodes = append(result.Nodes, graph.Node{ID: sectionID, Kind: graph.NodeSection, Name: name, Path: file.Path, StartLine: line, Meta: map[string]string{"level": lineString(level), "confidence": "extracted"}})
				result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeContains, From: documentID, To: sectionID, Meta: map[string]string{"confidence": "extracted"}})
			}
		}
		for _, match := range markdownLink.FindAllStringSubmatch(text, -1) {
			target := strings.TrimSpace(match[1])
			if target == "" {
				continue
			}
			refID := graph.ContentID("document-ref", target)
			result.Nodes = append(result.Nodes, graph.Node{ID: refID, Kind: graph.NodeDocument, Name: target, Meta: map[string]string{"reference": "true", "confidence": "extracted"}})
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeCites, From: documentID, To: refID, Meta: map[string]string{"line": lineString(line), "confidence": "extracted"}})
		}
	}
}

func markdownFence(text string) string {
	if strings.HasPrefix(text, "```") {
		return "```"
	}
	if strings.HasPrefix(text, "~~~") {
		return "~~~"
	}
	return ""
}

func analyzeSQL(file scan.File, content string, result *lang.AnalysisResult) {
	schemaID := graph.ContentID("schema", file.Path)
	result.Nodes = append(result.Nodes, graph.Node{ID: schemaID, Kind: graph.NodeSchema, Name: file.Path, Path: file.Path, Meta: map[string]string{"confidence": "extracted"}})
	result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(file.Path), To: schemaID, Meta: map[string]string{"confidence": "extracted"}})

	var tableID string
	scanner := bufio.NewScanner(strings.NewReader(content))
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if match := createTable.FindStringSubmatch(text); len(match) == 2 {
			name := strings.Trim(match[1], `"`+"`"+`[]`)
			tableID = graph.ContentID("table", file.Path, strings.ToLower(name))
			result.Nodes = append(result.Nodes, graph.Node{ID: tableID, Kind: graph.NodeTable, Name: name, Path: file.Path, StartLine: line, Meta: map[string]string{"confidence": "extracted"}})
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeContains, From: schemaID, To: tableID, Meta: map[string]string{"confidence": "extracted"}})
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(text), ")") {
			tableID = ""
			continue
		}
		if tableID == "" {
			continue
		}
		match := columnLine.FindStringSubmatch(text)
		if len(match) != 3 || sqlConstraint(match[1]) {
			continue
		}
		columnID := graph.ContentID("column", tableID, strings.ToLower(match[1]))
		result.Nodes = append(result.Nodes, graph.Node{ID: columnID, Kind: graph.NodeColumn, Name: match[1], Path: file.Path, StartLine: line, Meta: map[string]string{"type": match[2], "confidence": "extracted"}})
		result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeContains, From: tableID, To: columnID, Meta: map[string]string{"confidence": "extracted"}})
	}
}

func sqlConstraint(value string) bool {
	switch strings.ToLower(value) {
	case "primary", "foreign", "unique", "constraint", "check", "key":
		return true
	default:
		return false
	}
}

func lineString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
