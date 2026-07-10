package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	startMarker = "# reporavel-hook-start"
	endMarker   = "# reporavel-hook-end"
)

type Status struct {
	PostCommit   bool
	PostCheckout bool
}

func Install(startDir, executable string) (string, error) {
	hooksDir, err := findHooksDir(startDir)
	if err != nil {
		return "", err
	}
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	if err := installOne(filepath.Join(hooksDir, "post-commit"), hookSection(executable, false)); err != nil {
		return "", err
	}
	if err := installOne(filepath.Join(hooksDir, "post-checkout"), hookSection(executable, true)); err != nil {
		return "", err
	}
	return hooksDir, nil
}

func Uninstall(startDir string) (string, error) {
	hooksDir, err := findHooksDir(startDir)
	if err != nil {
		return "", err
	}
	for _, name := range []string{"post-commit", "post-checkout"} {
		if err := uninstallOne(filepath.Join(hooksDir, name)); err != nil {
			return "", err
		}
	}
	return hooksDir, nil
}

func Check(startDir string) (Status, error) {
	hooksDir, err := findHooksDir(startDir)
	if err != nil {
		return Status{}, err
	}
	return Status{
		PostCommit:   containsMarker(filepath.Join(hooksDir, "post-commit")),
		PostCheckout: containsMarker(filepath.Join(hooksDir, "post-checkout")),
	}, nil
}

func installOne(path, section string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(data)
	if strings.Contains(content, startMarker) {
		updated, err := replaceSection(content, section)
		if err != nil {
			return err
		}
		content = updated
	} else if content == "" {
		content = "#!/bin/sh\n\n" + section
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + section
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o755)
}

func uninstallOne(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	start := strings.Index(content, startMarker)
	if start < 0 {
		return nil
	}
	endRel := strings.Index(content[start:], endMarker)
	if endRel < 0 {
		return fmt.Errorf("%s contains an unterminated RepoRavel hook section", path)
	}
	end := start + endRel + len(endMarker)
	content = strings.TrimSpace(content[:start] + content[end:])
	if content == "" || content == "#!/bin/sh" || content == "#!/bin/bash" {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(content+"\n"), 0o755)
}

func replaceSection(content, section string) (string, error) {
	start := strings.Index(content, startMarker)
	endRel := strings.Index(content[start:], endMarker)
	if endRel < 0 {
		return "", errors.New("unterminated RepoRavel hook section")
	}
	end := start + endRel + len(endMarker)
	return strings.TrimRight(content[:start], "\n") + "\n\n" + section + strings.TrimLeft(content[end:], "\n"), nil
}

func containsMarker(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), startMarker)
}

func hookSection(executable string, checkout bool) string {
	guard := ""
	if checkout {
		guard = "[ \"$3\" = \"1\" ] || exit 0\n"
	}
	return startMarker + "\n" +
		guard +
		"REPORAVEL_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0\n" +
		"(cd \"$REPORAVEL_ROOT\" && " + shellQuote(executable) + " build . >>\"${TMPDIR:-/tmp}/reporavel-hook.log\" 2>&1) &\n" +
		endMarker + "\n"
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func findHooksDir(start string) (string, error) {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		info, statErr := os.Stat(gitPath)
		if statErr == nil {
			gitDir := gitPath
			if !info.IsDir() {
				data, readErr := os.ReadFile(gitPath)
				if readErr != nil {
					return "", readErr
				}
				line := strings.TrimSpace(string(data))
				value, ok := strings.CutPrefix(line, "gitdir:")
				if !ok {
					return "", fmt.Errorf("invalid .git file at %s", gitPath)
				}
				gitDir = strings.TrimSpace(value)
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
				if common, commonErr := os.ReadFile(filepath.Join(gitDir, "commondir")); commonErr == nil {
					commonDir := strings.TrimSpace(string(common))
					if !filepath.IsAbs(commonDir) {
						commonDir = filepath.Join(gitDir, commonDir)
					}
					gitDir = filepath.Clean(commonDir)
				}
			}
			return filepath.Join(gitDir, "hooks"), nil
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", errors.New("not inside a Git repository")
}
