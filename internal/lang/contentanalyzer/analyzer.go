package contentanalyzer

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
)

var (
	markdownLink = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
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
	if a.language == "sql" {
		sources := make([]sqlSource, 0, len(files))
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(file.AbsPath)
			if err != nil {
				return nil, err
			}
			sources = append(sources, sqlSource{file: file, content: string(data)})
		}
		analyzeSQLSources(sources, result)
		return result, nil
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(file.AbsPath)
		if err != nil {
			return nil, err
		}
		analyzeMarkdown(file, string(data), result)
	}
	return result, nil
}

func analyzeMarkdown(file scan.File, content string, result *lang.AnalysisResult) {
	documentID := graph.ContentID("document", file.Path)
	result.Nodes = append(result.Nodes, graph.Node{ID: documentID, Kind: graph.NodeDocument, Name: file.Path, Path: file.Path, Meta: extractedMeta(file.Path, 1)})
	result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeDefines, From: graph.FileID(file.Path), To: documentID, Meta: extractedMeta(file.Path, 1)})

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
				meta := extractedMeta(file.Path, line)
				meta["level"] = lineString(level)
				result.Nodes = append(result.Nodes, graph.Node{ID: sectionID, Kind: graph.NodeSection, Name: name, Path: file.Path, StartLine: line, Meta: meta})
				result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeContains, From: documentID, To: sectionID, Meta: extractedMeta(file.Path, line)})
			}
		}
		for _, match := range markdownLink.FindAllStringSubmatch(text, -1) {
			target := strings.TrimSpace(match[1])
			if target == "" {
				continue
			}
			refID := graph.ContentID("document-ref", target)
			meta := extractedMeta(file.Path, line)
			meta["reference"] = "true"
			result.Nodes = append(result.Nodes, graph.Node{ID: refID, Kind: graph.NodeDocument, Name: target, Meta: meta})
			result.Edges = append(result.Edges, graph.Edge{Kind: graph.EdgeCites, From: documentID, To: refID, Meta: extractedMeta(file.Path, line)})
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

func extractedMeta(path string, line int) map[string]string {
	evidence := path
	if line > 0 {
		evidence += ":" + lineString(line)
	}
	return map[string]string{"confidence": "extracted", "evidence": evidence, "line": lineString(line), "path": path}
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
