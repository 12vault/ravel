package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/12vault/ravel/skills"
)

func TestRefreshExistingUpdatesSkillsAndOwnedInstructions(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	projectSkill := SkillOptions{Platform: "codex", Project: true, ProjectDir: project, HomeDir: home}
	projectSkillPath, err := InstallSkill(projectSkill)
	if err != nil {
		t.Fatal(err)
	}
	userSkill := SkillOptions{Platform: "claude", HomeDir: home, ProjectDir: project}
	userSkillPath, err := InstallSkill(userSkill)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{projectSkillPath, userSkillPath} {
		if err := os.WriteFile(path, []byte("old skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "VERSION"), []byte("v0.0.1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := InstallIntegration(IntegrationOptions{Platform: "codex", ProjectDir: project, Executable: "/old/ravel"}); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(project, "AGENTS.md")
	staleCodex := agentsStart + "\n## RepoRavel\n\nOld coding guidance.\n" + agentsEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(staleCodex), 0o644); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(project, ".codex", "hooks.json")
	hooksBefore, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}

	copilotPath := filepath.Join(project, ".github", "copilot-instructions.md")
	if err := os.MkdirAll(filepath.Dir(copilotPath), 0o755); err != nil {
		t.Fatal(err)
	}
	staleCopilot := agentsStart + "\n## Ravel for GitHub Copilot\n\nOld coding guidance.\n" + agentsEnd + "\n"
	if err := os.WriteFile(copilotPath, []byte(staleCopilot), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RefreshExisting(RefreshOptions{ProjectDir: project, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 2 {
		t.Fatalf("refreshed skills = %v, want project Codex and user Claude", result.Skills)
	}
	for _, path := range []string{projectSkillPath, userSkillPath} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != string(skills.Ravel) {
			t.Fatalf("refreshed skill %s = %q, %v", path, data, readErr)
		}
	}
	for _, path := range []string{agentsPath, copilotPath} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(data), "non-trivial coding task") || !strings.Contains(string(data), "ravel affected") {
			t.Fatalf("refreshed instructions %s = %q, %v", path, data, readErr)
		}
	}
	hooksAfter, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(hooksAfter) != string(hooksBefore) {
		t.Fatalf("automatic refresh rewrote existing hook command:\nbefore: %s\nafter: %s", hooksBefore, hooksAfter)
	}
	if fileExists(filepath.Join(project, ".cursor", "rules", "ravel.mdc")) {
		t.Fatal("automatic refresh created an uninstalled Cursor integration")
	}
	if fileExists(filepath.Join(project, ".claude", "settings.json")) {
		t.Fatal("user-wide Claude skill caused a project integration to be created")
	}
}

func TestRefreshExistingDoesNotCreateIntegrationFromProjectSkill(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	opts := SkillOptions{Platform: "cursor", Project: true, ProjectDir: project, HomeDir: home}
	skillPath, err := InstallSkill(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("old skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(skillPath), "VERSION"), []byte("v0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RefreshExisting(RefreshOptions{ProjectDir: project, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || len(result.Integrations) != 0 {
		t.Fatalf("refresh result = %#v, want one skill and no integrations", result)
	}
	if fileExists(filepath.Join(project, ".cursor", "rules", "ravel.mdc")) {
		t.Fatal("automatic refresh created Cursor rules without a prior integration")
	}
}

func TestRefreshExistingUpdatesEveryNativeIntegration(t *testing.T) {
	for _, platform := range NativePlatforms() {
		platform := platform
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			project := t.TempDir()
			paths, err := InstallIntegration(IntegrationOptions{Platform: platform, ProjectDir: project, Executable: "/old/ravel"})
			if err != nil {
				t.Fatal(err)
			}
			instructionPath := integrationInstructionPath(project, platform)
			data, err := os.ReadFile(instructionPath)
			if err != nil {
				t.Fatal(err)
			}
			stale := strings.Replace(string(data), instructionBody(), "Old relationship-only guidance for `.reporavel/graph.json`.", 1)
			if stale == string(data) {
				t.Fatalf("could not make %s instructions stale", platform)
			}
			if err := os.WriteFile(instructionPath, []byte(stale), 0o644); err != nil {
				t.Fatal(err)
			}
			preserved := map[string]string{}
			for _, path := range paths {
				if path == instructionPath {
					continue
				}
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				preserved[path] = string(content)
			}

			if _, err := RefreshExisting(RefreshOptions{ProjectDir: project, HomeDir: home}); err != nil {
				t.Fatal(err)
			}
			refreshed, err := os.ReadFile(instructionPath)
			if err != nil || !strings.Contains(string(refreshed), instructionBody()) {
				t.Fatalf("%s instructions were not refreshed: %q, %v", platform, refreshed, err)
			}
			for path, before := range preserved {
				after, readErr := os.ReadFile(path)
				if readErr != nil || string(after) != before {
					t.Fatalf("%s automatic refresh changed companion file %s:\nbefore: %s\nafter: %s\nerror: %v", platform, path, before, after, readErr)
				}
			}
		})
	}
}

func integrationInstructionPath(root, platform string) string {
	switch normalizeIntegration(platform) {
	case "codex", "opencode":
		return filepath.Join(root, "AGENTS.md")
	case "claude":
		return filepath.Join(root, "CLAUDE.md")
	case "cursor":
		return filepath.Join(root, ".cursor", "rules", "ravel.mdc")
	case "vscode", "copilot":
		return filepath.Join(root, ".github", "copilot-instructions.md")
	case "gemini":
		return filepath.Join(root, "GEMINI.md")
	default:
		return ""
	}
}

func TestRefreshExistingPreservesSameVersionSkillCustomization(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	opts := SkillOptions{Platform: "codex", Project: true, ProjectDir: project, HomeDir: home}
	skillPath, err := InstallSkill(opts)
	if err != nil {
		t.Fatal(err)
	}
	custom := []byte("custom same-version skill\n")
	if err := os.WriteFile(skillPath, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RefreshExisting(RefreshOptions{ProjectDir: project, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 0 {
		t.Fatalf("same-version customized skill was refreshed: %v", result.Skills)
	}
	data, err := os.ReadFile(skillPath)
	if err != nil || string(data) != string(custom) {
		t.Fatalf("same-version customized skill = %q, %v", data, err)
	}
}

func TestRefreshExistingDoesNotRewriteCurrentInstructions(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if _, err := InstallIntegration(IntegrationOptions{Platform: "codex", ProjectDir: project, Executable: "/opt/ravel"}); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(project, "AGENTS.md")
	baseline := time.Unix(946684800, 0)
	if err := os.Chtimes(agentsPath, baseline, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshExisting(RefreshOptions{ProjectDir: project, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(baseline) {
		t.Fatalf("current instructions were rewritten: modTime=%v", info.ModTime())
	}
}

func TestUpdateOwnedSectionPreservesSurroundingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	content := "# Before\n\n" + agentsStart + "\nold\n" + agentsEnd + "\n\n# After\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateOwnedSection(path, codexInstructions()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "# Before\n\n") || !strings.HasSuffix(text, "\n\n# After\n") || !strings.Contains(text, instructionBody()) {
		t.Fatalf("updated section did not preserve surrounding content:\n%s", text)
	}
}
