package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/12vault/ravel/skills"
)

const (
	agentsStart = "<!-- ravel:start -->"
	agentsEnd   = "<!-- ravel:end -->"
)

var platformPaths = map[string]string{
	"agents":      ".agents/skills/ravel/SKILL.md",
	"antigravity": ".agents/skills/ravel/SKILL.md",
	"aider":       ".aider/ravel/SKILL.md",
	"claude":      ".claude/skills/ravel/SKILL.md",
	"codebuddy":   ".codebuddy/skills/ravel/SKILL.md",
	"codex":       ".codex/skills/ravel/SKILL.md",
	"copilot":     ".copilot/skills/ravel/SKILL.md",
	"cursor":      ".cursor/skills/ravel/SKILL.md",
	"devin":       ".config/devin/skills/ravel/SKILL.md",
	"droid":       ".factory/skills/ravel/SKILL.md",
	"gemini":      ".gemini/skills/ravel/SKILL.md",
	"hermes":      ".hermes/skills/ravel/SKILL.md",
	"kilo":        ".config/kilo/skills/ravel/SKILL.md",
	"kimi":        ".kimi/skills/ravel/SKILL.md",
	"kiro":        ".kiro/skills/ravel/SKILL.md",
	"openclaw":    ".openclaw/skills/ravel/SKILL.md",
	"opencode":    ".config/opencode/skills/ravel/SKILL.md",
	"pi":          ".pi/agent/skills/ravel/SKILL.md",
	"trae":        ".trae/skills/ravel/SKILL.md",
	"trae-cn":     ".trae-cn/skills/ravel/SKILL.md",
	"vscode":      ".copilot/skills/ravel/SKILL.md",
}

var platformAliases = map[string]string{
	"amp":     "agents",
	"claw":    "openclaw",
	"factory": "droid",
	"skills":  "agents",
	"windows": "claude",
}

type SkillOptions struct {
	Platform   string
	Project    bool
	ProjectDir string
	HomeDir    string
}

func Platforms() []string {
	names := make([]string, 0, len(platformPaths)+len(platformAliases))
	for name := range platformPaths {
		names = append(names, name)
	}
	for name := range platformAliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func InstallSkill(opts SkillOptions) (string, error) {
	dst, err := skillDestination(opts)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	support, err := skills.SupportFiles()
	if err != nil {
		return "", err
	}
	tmpBundle, err := os.MkdirTemp(filepath.Dir(dst), ".ravel-bundle-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpBundle)
	names := make([]string, 0, len(support))
	for name := range support {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(tmpBundle, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(name, "scripts/") && strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, support[name], mode); err != nil {
			return "", err
		}
	}
	for _, directory := range []string{"references", "agents", "scripts"} {
		source := filepath.Join(tmpBundle, directory)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		if err := replaceDirectory(source, filepath.Join(filepath.Dir(dst), directory)); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"VERSION", "THIRD_PARTY_NOTICES.md"} {
		data, ok := support[name]
		if !ok {
			return "", fmt.Errorf("embedded skill bundle omits %s", name)
		}
		if err := writeFileIfChanged(filepath.Join(filepath.Dir(dst), name), data, 0o644); err != nil {
			return "", err
		}
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, skills.Ravel, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, statErr := os.Stat(dst); statErr == nil {
			if removeErr := os.Remove(dst); removeErr == nil {
				err = os.Rename(tmp, dst)
			}
		}
		if err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
	}
	return dst, nil
}

func replaceDirectory(source, destination string) error {
	backup := destination + ".previous"
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	hadDestination := false
	if _, err := os.Stat(destination); err == nil {
		hadDestination = true
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		if hadDestination {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	return os.RemoveAll(backup)
}

func UninstallSkill(opts SkillOptions) (string, bool, error) {
	dst, err := skillDestination(opts)
	if err != nil {
		return "", false, err
	}
	removed := false
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return dst, false, err
	} else if err == nil {
		removed = true
	}
	for _, name := range []string{"VERSION", "THIRD_PARTY_NOTICES.md"} {
		path := filepath.Join(filepath.Dir(dst), name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return dst, false, err
		} else if err == nil {
			removed = true
		}
	}
	for _, directory := range []string{"references", "agents", "scripts"} {
		path := filepath.Join(filepath.Dir(dst), directory)
		if _, err := os.Stat(path); err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return dst, false, err
		}
		if err := os.RemoveAll(path); err != nil {
			return dst, false, err
		}
	}
	removeEmptyParents(filepath.Dir(dst), scopeRoot(opts))
	return dst, removed, nil
}

func skillDestination(opts SkillOptions) (string, error) {
	platform := strings.ToLower(strings.TrimSpace(opts.Platform))
	if platform == "" {
		platform = "claude"
	}
	if alias, ok := platformAliases[platform]; ok {
		platform = alias
	}
	rel, ok := platformPaths[platform]
	if !ok {
		return "", fmt.Errorf("unknown platform %q (choose from: %s)", opts.Platform, strings.Join(Platforms(), ", "))
	}

	root := scopeRoot(opts)
	if root == "" {
		return "", errors.New("cannot determine install directory")
	}
	if opts.Project {
		switch platform {
		case "opencode":
			rel = ".opencode/skills/ravel/SKILL.md"
		case "devin":
			rel = ".devin/skills/ravel/SKILL.md"
		case "kilo":
			rel = ".kilo/skills/ravel/SKILL.md"
		}
	}
	if platform == "agents" && !opts.Project && strings.EqualFold(opts.Platform, "amp") {
		rel = ".config/agents/skills/ravel/SKILL.md"
	}
	if platform == "antigravity" && !opts.Project {
		rel = ".gemini/config/skills/ravel/SKILL.md"
	}
	if platform == "hermes" && !opts.Project && runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "hermes", "skills", "ravel", "SKILL.md"), nil
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func scopeRoot(opts SkillOptions) string {
	if opts.Project {
		if opts.ProjectDir != "" {
			return opts.ProjectDir
		}
		cwd, _ := os.Getwd()
		return cwd
	}
	if opts.HomeDir != "" {
		return opts.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func removeEmptyParents(dir, stop string) {
	stop = filepath.Clean(stop)
	for dir != stop && strings.HasPrefix(filepath.Clean(dir), stop+string(filepath.Separator)) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

type CodexOptions struct {
	ProjectDir string
	Executable string
}

func InstallCodex(opts CodexOptions) ([]string, error) {
	root := opts.ProjectDir
	if root == "" {
		root = "."
	}
	exe := opts.Executable
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}

	agentsPath := filepath.Join(root, "AGENTS.md")
	if err := updateOwnedSection(agentsPath, codexInstructions()); err != nil {
		return nil, err
	}
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if err := installCodexHook(hooksPath, exe); err != nil {
		return nil, err
	}
	return []string{agentsPath, hooksPath}, nil
}

func UninstallCodex(projectDir string) ([]string, error) {
	if projectDir == "" {
		projectDir = "."
	}
	agentsPath := filepath.Join(projectDir, "AGENTS.md")
	if err := removeOwnedSection(agentsPath); err != nil {
		return nil, err
	}
	hooksPath := filepath.Join(projectDir, ".codex", "hooks.json")
	if err := uninstallCodexHook(hooksPath); err != nil {
		return nil, err
	}
	return []string{agentsPath, hooksPath}, nil
}

func AssistantHook(root, platform string) ([]byte, error) {
	if root == "" {
		root = "."
	}
	if _, err := os.Stat(filepath.Join(root, ".reporavel", "graph.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	message := "Ravel graph found. " + instructionBody()
	if strings.EqualFold(platform, "claude") {
		return json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     "PreToolUse",
			"additionalContext": message,
		}})
	}
	return json.Marshal(map[string]string{"systemMessage": message})
}

func codexInstructions() string {
	return agentsStart + "\n" +
		"## RepoRavel\n\n" +
		instructionBody() + "\n" +
		agentsEnd + "\n"
}

func updateOwnedSection(path, section string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(data)
	if start := strings.Index(content, agentsStart); start >= 0 {
		endRel := strings.Index(content[start:], agentsEnd)
		if endRel < 0 {
			return fmt.Errorf("%s contains an unterminated RepoRavel section", path)
		}
		end := start + endRel + len(agentsEnd)
		prefix := strings.TrimRight(content[:start], "\n")
		suffix := strings.TrimLeft(content[end:], "\n")
		content = prefix
		if content != "" {
			content += "\n\n"
		}
		content += strings.TrimSpace(section)
		if suffix != "" {
			content += "\n\n" + suffix
		}
	} else if strings.TrimSpace(content) == "" {
		content = section
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + section
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileIfChanged(path, []byte(content), 0o644)
}

func removeOwnedSection(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	start := strings.Index(content, agentsStart)
	if start < 0 {
		return nil
	}
	endRel := strings.Index(content[start:], agentsEnd)
	if endRel < 0 {
		return fmt.Errorf("%s contains an unterminated RepoRavel section", path)
	}
	end := start + endRel + len(agentsEnd)
	content = strings.TrimSpace(content[:start] + content[end:])
	if content == "" {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(content+"\n"), 0o644) // #nosec G703 -- path is a platform integration file under the caller-selected project root.
}

func installCodexHook(path, executable string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks := objectAt(root, "hooks")
	current := arrayAt(hooks, "PreToolUse")
	filtered := withoutRavel(current)
	filtered = append(filtered, map[string]any{
		"matcher": "Bash",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": shellQuote(executable) + " assistant-hook",
		}},
	})
	hooks["PreToolUse"] = filtered
	root["hooks"] = hooks
	return writeJSONObject(path, root)
}

func uninstallCodexHook(path string) error {
	root, err := readJSONObject(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	remaining := withoutRavel(arrayAt(hooks, "PreToolUse"))
	if len(remaining) == 0 {
		delete(hooks, "PreToolUse")
	} else {
		hooks["PreToolUse"] = remaining
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	if len(root) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(filepath.Dir(path))
		return nil
	}
	return writeJSONObject(path, root)
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func writeJSONObject(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileIfChanged(path, data, 0o644)
}

func writeFileIfChanged(path string, data []byte, mode os.FileMode) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, mode) // #nosec G703 -- installer destinations are derived from caller-selected project or home roots.
}

func objectAt(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func arrayAt(parent map[string]any, key string) []any {
	if value, ok := parent[key].([]any); ok {
		return value
	}
	return nil
}

func withoutRavel(values []any) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		data, _ := json.Marshal(value)
		if !bytes.Contains(bytes.ToLower(data), []byte("ravel")) {
			result = append(result, value)
		}
	}
	return result
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
