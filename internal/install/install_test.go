package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/skills"
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
				if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "references", "workflows.md")); err != nil {
					t.Fatalf("installed references missing: %v", err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "agents", "code-analyzer.md")); err != nil {
					t.Fatalf("installed agents missing: %v", err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "VERSION")); err != nil {
					t.Fatalf("installed VERSION missing: %v", err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "THIRD_PARTY_NOTICES.md")); err != nil {
					t.Fatalf("installed notices missing: %v", err)
				}
				bootstrap := filepath.Join(filepath.Dir(dst), "scripts", "bootstrap.sh")
				if info, err := os.Stat(bootstrap); err != nil || info.Mode()&0o111 == 0 {
					t.Fatalf("installed executable bootstrap missing: %v, mode %v", err, infoMode(info))
				}
				if _, removed, err := UninstallSkill(opts); err != nil || !removed {
					t.Fatalf("UninstallSkill() = removed %v, err %v", removed, err)
				}
			}
		})
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func TestReplaceDirectoryRestoresExistingBundleOnFailure(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "references")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "existing.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceDirectory(filepath.Join(root, "missing"), destination); err == nil {
		t.Fatal("expected replacement failure")
	}
	data, err := os.ReadFile(filepath.Join(destination, "existing.md"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing bundle was not restored: %q, %v", data, err)
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
	for _, want := range []string{"non-trivial coding task", "ravel context", "ravel affected", "read the returned source", "trivial known-file"} {
		if !strings.Contains(string(agents), want) {
			t.Fatalf("AGENTS.md is missing coding-context guidance %q:\n%s", want, agents)
		}
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
	data, err := AssistantHook(root, "codex")
	if err != nil || data != nil {
		t.Fatalf("AssistantHook without graph = %q, %v", data, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".reporavel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".reporavel", "graph.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err = AssistantHook(root, "codex")
	if err != nil || !strings.Contains(string(data), "systemMessage") {
		t.Fatalf("AssistantHook with graph = %q, %v", data, err)
	}
	for _, want := range []string{"non-trivial coding task", "ravel context", "ravel affected", "read the returned source"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("AssistantHook output is missing %q: %s", want, data)
		}
	}
}

func TestNativeProjectIntegrationsAreIdempotentAndReversible(t *testing.T) {
	for _, platform := range NativePlatforms() {
		platform := platform
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			paths, err := InstallIntegration(IntegrationOptions{Platform: platform, ProjectDir: root, Executable: "/opt/ravel"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := InstallIntegration(IntegrationOptions{Platform: platform, ProjectDir: root, Executable: "/opt/ravel"}); err != nil {
				t.Fatalf("second install: %v", err)
			}
			for _, path := range paths {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("missing integration file %s: %v", path, err)
				}
			}
			var foundCodingGuidance bool
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err == nil && strings.Contains(string(data), "non-trivial coding task") && strings.Contains(string(data), "ravel affected") {
					foundCodingGuidance = true
				}
			}
			if !foundCodingGuidance {
				t.Fatalf("%s integration did not install coding-context guidance", platform)
			}
			removed, err := UninstallIntegration(platform, root)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range removed {
				data, err := os.ReadFile(path)
				if err == nil && strings.Contains(strings.ToLower(string(data)), "ravel") {
					t.Fatalf("Ravel content remains in %s:\n%s", path, data)
				}
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestClaudeHookUsesAdditionalContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reporavel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".reporavel", "graph.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := AssistantHook(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "additionalContext") || !strings.Contains(string(data), "PreToolUse") {
		t.Fatalf("unexpected Claude hook output: %s", data)
	}
}
