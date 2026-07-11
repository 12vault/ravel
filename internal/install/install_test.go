package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12ya/reporavel/skills"
)

func TestInstallAndUninstallSkillForEveryPlatform(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	for _, platform := range Platforms() {
		platform := platform
		t.Run(platform, func(t *testing.T) {
			for _, projectScope := range []bool{false, true} {
				opts := SkillOptions{
					Platform:   platform,
					Project:    projectScope,
					ProjectDir: project,
					HomeDir:    home,
				}
				dst, err := InstallSkill(opts)
				if err != nil {
					t.Fatalf("InstallSkill(%q, project=%v): %v", platform, projectScope, err)
				}
				if _, err := InstallSkill(opts); err != nil {
					t.Fatalf("second InstallSkill(%q, project=%v): %v", platform, projectScope, err)
				}
				data, err := os.ReadFile(dst)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != string(skills.Ravel) {
					t.Fatalf("installed skill differs from embedded skill")
				}
				if _, removed, err := UninstallSkill(opts); err != nil || !removed {
					t.Fatalf("UninstallSkill() = removed %v, err %v", removed, err)
				}
			}
		})
	}
}

func TestUninstallCodexRemovesFilesItCreated(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallCodex(CodexOptions{ProjectDir: root, Executable: "/opt/ravel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCodex(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, ".codex", "hooks.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after uninstall: %v", path, err)
		}
	}
}

func TestInstallCodexPreservesExistingConfigurationAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"other-tool"}]}]},"keep":true}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := CodexOptions{ProjectDir: root, Executable: "/opt/tools/ravel"}
	if _, err := InstallCodex(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodex(opts); err != nil {
		t.Fatal(err)
	}

	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "# Existing") || strings.Count(string(agents), agentsStart) != 1 {
		t.Fatalf("unexpected AGENTS.md:\n%s", agents)
	}

	var hooks map[string]any
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &hooks); err != nil {
		t.Fatal(err)
	}
	if hooks["keep"] != true {
		t.Fatalf("unrelated hook configuration was not preserved: %#v", hooks)
	}
	pre := hooks["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected one existing and one Ravel hook, got %d", len(pre))
	}

	if _, err := UninstallCodex(root); err != nil {
		t.Fatal(err)
	}
	agents, err = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agents) != "# Existing\n" {
		t.Fatalf("uninstall changed unrelated instructions: %q", agents)
	}
	data, err = os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "ravel") || !strings.Contains(string(data), "other-tool") {
		t.Fatalf("unexpected hooks after uninstall:\n%s", data)
	}
}

func TestAssistantHookOnlyRespondsWhenGraphExists(t *testing.T) {
	root := t.TempDir()
	data, err := AssistantHook(root)
	if err != nil || data != nil {
		t.Fatalf("AssistantHook without graph = %q, %v", data, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".reporavel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".reporavel", "graph.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err = AssistantHook(root)
	if err != nil || !strings.Contains(string(data), "systemMessage") {
		t.Fatalf("AssistantHook with graph = %q, %v", data, err)
	}
}
