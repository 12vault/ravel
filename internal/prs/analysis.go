// Package prs overlays GitHub pull-request changes on a Ravel graph. The
// analysis is pure and accepts decoded PR metadata so live GitHub collection
// remains a thin, replaceable CLI adapter.
package prs

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
)

type PullRequest struct {
	Number            int     `json:"number"`
	Title             string  `json:"title"`
	URL               string  `json:"url,omitempty"`
	HeadRefName       string  `json:"headRefName,omitempty"`
	BaseRefName       string  `json:"baseRefName,omitempty"`
	IsDraft           bool    `json:"isDraft,omitempty"`
	ReviewDecision    string  `json:"reviewDecision,omitempty"`
	Files             []File  `json:"files"`
	StatusCheckRollup []Check `json:"statusCheckRollup,omitempty"`
}

type File struct {
	Path      string `json:"path"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

type Check struct {
	Name       string `json:"name,omitempty"`
	Context    string `json:"context,omitempty"`
	State      string `json:"state,omitempty"`
	Status     string `json:"status,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
}

type Analysis struct {
	Version      int        `json:"version"`
	PullRequests []Result   `json:"pullRequests"`
	Conflicts    []Conflict `json:"conflicts"`
}

type Result struct {
	Number          int      `json:"number"`
	Title           string   `json:"title"`
	URL             string   `json:"url,omitempty"`
	HeadRefName     string   `json:"headRefName,omitempty"`
	BaseRefName     string   `json:"baseRefName,omitempty"`
	Draft           bool     `json:"draft,omitempty"`
	ReviewDecision  string   `json:"reviewDecision,omitempty"`
	CI              string   `json:"ci"`
	ChangedFiles    []string `json:"changedFiles"`
	MappedFiles     []string `json:"mappedFiles"`
	Communities     []string `json:"communities"`
	CommunityIDs    []string `json:"communityIds"`
	AffectedNodeIDs []string `json:"affectedNodeIds"`
	ImpactTruncated bool     `json:"impactTruncated,omitempty"`
}

type Conflict struct {
	LeftPR            int      `json:"leftPr"`
	RightPR           int      `json:"rightPr"`
	Risk              string   `json:"risk"`
	SharedFiles       []string `json:"sharedFiles,omitempty"`
	SharedCommunities []string `json:"sharedCommunities,omitempty"`
}

func Analyze(g graph.Graph, pullRequests []PullRequest) Analysis {
	filesByPath := map[string][]graph.Node{}
	communityNames := map[string]string{}
	for _, node := range g.Nodes {
		if node.Kind == graph.NodeFile && node.Path != "" {
			path := cleanPath(node.Path)
			filesByPath[path] = append(filesByPath[path], node)
		}
		if id := node.Meta[community.MetaKey]; id != "" {
			name := node.Meta[community.MetaLabelKey]
			if name == "" {
				name = node.Meta[community.MetaNameKey]
			}
			if name == "" {
				name = id
			}
			communityNames[id] = name
		}
	}
	idx := query.NewIndex(g)
	results := make([]Result, 0, len(pullRequests))
	for _, pr := range pullRequests {
		result := Result{
			Number: pr.Number, Title: strings.TrimSpace(pr.Title), URL: pr.URL,
			HeadRefName: pr.HeadRefName, BaseRefName: pr.BaseRefName, Draft: pr.IsDraft,
			ReviewDecision: pr.ReviewDecision, CI: checkSummary(pr.StatusCheckRollup),
		}
		fileSet := map[string]bool{}
		mappedSet := map[string]bool{}
		communitySet := map[string]bool{}
		affectedSet := map[string]bool{}
		for _, file := range pr.Files {
			path := cleanPath(file.Path)
			if path == "" {
				continue
			}
			fileSet[path] = true
			matches := filesByPath[path]
			if len(matches) != 1 {
				continue
			}
			matched := matches[0]
			mappedSet[path] = true
			if id := matched.Meta[community.MetaKey]; id != "" {
				communitySet[id] = true
			}
			impact, err := idx.Affected(matched.ID, query.RetrieveOptions{
				MaxDepth: 2, MaxNodes: 500, TokenBudget: 100_000, HubDegreeThreshold: -1,
			})
			if err != nil {
				continue
			}
			if impact.Stats.Truncated {
				result.ImpactTruncated = true
			}
			for _, node := range impact.Nodes {
				affectedSet[node.ID] = true
			}
		}
		result.ChangedFiles = sortedSet(fileSet)
		result.MappedFiles = sortedSet(mappedSet)
		result.CommunityIDs = sortedSet(communitySet)
		for _, id := range result.CommunityIDs {
			result.Communities = append(result.Communities, communityNames[id])
		}
		result.AffectedNodeIDs = sortedSet(affectedSet)
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Number < results[j].Number })
	return Analysis{Version: 1, PullRequests: results, Conflicts: conflicts(results)}
}

func conflicts(results []Result) []Conflict {
	var out []Conflict
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			sharedFiles := intersection(results[i].MappedFiles, results[j].MappedFiles)
			sharedCommunityIDs := intersection(results[i].CommunityIDs, results[j].CommunityIDs)
			if len(sharedFiles) == 0 && len(sharedCommunityIDs) == 0 {
				continue
			}
			sharedNames := make([]string, 0, len(sharedCommunityIDs))
			for _, id := range sharedCommunityIDs {
				sharedNames = append(sharedNames, communityNameForResult(results[i], id))
			}
			risk := "community"
			if len(sharedFiles) > 0 {
				risk = "file"
			}
			out = append(out, Conflict{
				LeftPR: results[i].Number, RightPR: results[j].Number, Risk: risk,
				SharedFiles: sharedFiles, SharedCommunities: sharedNames,
			})
		}
	}
	return out
}

func communityNameForResult(result Result, id string) string {
	for i, candidate := range result.CommunityIDs {
		if candidate == id && i < len(result.Communities) {
			return result.Communities[i]
		}
	}
	return id
}

func WriteText(w io.Writer, analysis Analysis, conflictsOnly bool) error {
	if !conflictsOnly {
		for _, result := range analysis.PullRequests {
			state := result.CI
			if result.Draft {
				state += ", draft"
			}
			if result.ReviewDecision != "" {
				state += ", review=" + strings.ToLower(result.ReviewDecision)
			}
			if _, err := fmt.Fprintf(w, "#%d %s [%s]\n", result.Number, result.Title, state); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "  files: %d changed, %d mapped · communities: %d · affected nodes: %d", len(result.ChangedFiles), len(result.MappedFiles), len(result.CommunityIDs), len(result.AffectedNodeIDs)); err != nil {
				return err
			}
			if result.ImpactTruncated {
				if _, err := fmt.Fprint(w, " (truncated)"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}
	if len(analysis.Conflicts) == 0 {
		_, err := fmt.Fprintln(w, "No graph-overlap conflicts detected.")
		return err
	}
	if !conflictsOnly {
		if _, err := fmt.Fprintln(w, "Conflicts:"); err != nil {
			return err
		}
	}
	for _, conflict := range analysis.Conflicts {
		details := []string{}
		if len(conflict.SharedFiles) > 0 {
			details = append(details, "files="+strings.Join(conflict.SharedFiles, ","))
		}
		if len(conflict.SharedCommunities) > 0 {
			details = append(details, "communities="+strings.Join(conflict.SharedCommunities, ","))
		}
		if _, err := fmt.Fprintf(w, "  #%d ↔ #%d [%s overlap] %s\n", conflict.LeftPR, conflict.RightPR, conflict.Risk, strings.Join(details, " · ")); err != nil {
			return err
		}
	}
	return nil
}

func checkSummary(checks []Check) string {
	if len(checks) == 0 {
		return "ci=unknown"
	}
	pending := false
	for _, check := range checks {
		state := strings.ToUpper(strings.Join([]string{check.State, check.Status, check.Conclusion}, " "))
		for _, failure := range []string{"FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED"} {
			if strings.Contains(state, failure) {
				return "ci=failing"
			}
		}
		for _, waiting := range []string{"PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "WAITING"} {
			if strings.Contains(state, waiting) {
				pending = true
			}
		}
	}
	if pending {
		return "ci=pending"
	}
	return "ci=passing"
}

func cleanPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	return strings.TrimPrefix(path, "./")
}

func sortedSet(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func intersection(left, right []string) []string {
	rightSet := map[string]bool{}
	for _, value := range right {
		rightSet[value] = true
	}
	var shared []string
	for _, value := range left {
		if rightSet[value] {
			shared = append(shared, value)
		}
	}
	return shared
}
