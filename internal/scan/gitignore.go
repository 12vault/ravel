package scan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxIgnoreFileSize = 1 << 20

type ignoreMatcher struct {
	root  string
	rules []ignoreRule
}

type ignoreRule struct {
	base    string
	pattern string
	negated bool
	dirOnly bool
	matcher *regexp.Regexp
}

func newIgnoreMatcher(root string) (*ignoreMatcher, error) {
	matcher := &ignoreMatcher{root: root}
	if err := matcher.loadDir("."); err != nil {
		return nil, err
	}
	return matcher, nil
}

func (m *ignoreMatcher) loadDir(relativeDir string) error {
	path := filepath.Join(m.root, filepath.FromSlash(relativeDir), ".gitignore")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxIgnoreFileSize {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")), err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")), err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("read %s: file changed while opening", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")))
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), maxIgnoreFileSize)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		rule, ok, err := parseIgnoreRule(relativeDir, scanner.Text())
		if err != nil {
			return fmt.Errorf("parse %s:%d: %w", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")), lineNumber, err)
		}
		if ok {
			m.rules = append(m.rules, rule)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", filepath.ToSlash(filepath.Join(relativeDir, ".gitignore")), err)
	}
	return nil
}

func (m *ignoreMatcher) ignored(relativePath string, isDir bool) bool {
	relativePath = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relativePath)), "./")
	ignored := false
	for _, rule := range m.rules {
		candidate, ok := pathWithin(rule.base, relativePath)
		if !ok || (rule.dirOnly && !isDir) {
			continue
		}
		if rule.matcher.MatchString(candidate) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func parseIgnoreRule(base, line string) (ignoreRule, bool, error) {
	line = strings.TrimSuffix(line, "\r")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false, nil
	}
	escapedMarker := strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`)
	if escapedMarker {
		line = line[1:]
	}
	negated := !escapedMarker && strings.HasPrefix(line, "!")
	if negated {
		line = strings.TrimPrefix(line, "!")
	}
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	anchored := strings.HasPrefix(line, "/") || strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return ignoreRule{}, false, nil
	}
	body, err := gitGlobRegex(line)
	if err != nil {
		return ignoreRule{}, false, err
	}
	if anchored {
		body = "^" + body + "$"
	} else {
		body = `(^|/)` + body + `$`
	}
	compiled, err := regexp.Compile(body)
	if err != nil {
		return ignoreRule{}, false, err
	}
	return ignoreRule{base: cleanIgnoreBase(base), pattern: line, negated: negated, dirOnly: dirOnly, matcher: compiled}, true, nil
}

func gitGlobRegex(pattern string) (string, error) {
	runes := []rune(pattern)
	var result strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				for i+1 < len(runes) && runes[i+1] == '*' {
					i++
				}
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					result.WriteString(`(?:.*/)?`)
				} else {
					result.WriteString(`.*`)
				}
			} else {
				result.WriteString(`[^/]*`)
			}
		case '?':
			result.WriteString(`[^/]`)
		case '[':
			end := i + 1
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end == len(runes) {
				result.WriteString(`\[`)
				continue
			}
			class := string(runes[i+1 : end])
			if strings.HasPrefix(class, "!") {
				class = "^" + strings.TrimPrefix(class, "!")
			}
			result.WriteString("[" + class + "]")
			i = end
		case '\\':
			if i+1 < len(runes) {
				i++
				result.WriteString(regexp.QuoteMeta(string(runes[i])))
			} else {
				result.WriteString(`\\`)
			}
		default:
			result.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return result.String(), nil
}

func pathWithin(base, path string) (string, bool) {
	base = cleanIgnoreBase(base)
	if base == "." {
		return path, true
	}
	if path == base {
		return ".", true
	}
	prefix := base + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return strings.TrimPrefix(path, prefix), true
}

func cleanIgnoreBase(base string) string {
	base = filepath.ToSlash(filepath.Clean(base))
	if base == "" || base == "/" {
		return "."
	}
	return base
}
