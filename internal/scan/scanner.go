package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/12vault/ravel/internal/config"
)

type File struct {
	Path     string    `json:"path"`
	AbsPath  string    `json:"-"`
	Language string    `json:"language"`
	Size     int64     `json:"size"`
	Hash     string    `json:"hash"`
	ModTime  time.Time `json:"modifiedAt"`
}

type Ignored struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Result struct {
	Root       string    `json:"root"`
	Files      []File    `json:"files"`
	Ignored    []Ignored `json:"ignored"`
	TotalBytes int64     `json:"totalBytes"`
}

func Scan(root string, cfg config.Config) (Result, error) {
	return ScanWithProgress(root, cfg, nil)
}

// ScanWithProgress scans root and reports each filesystem entry before it is
// inspected. The callback is optional and is intended for live CLI feedback.
func ScanWithProgress(root string, cfg config.Config, visit func(path string, files int)) (Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve scan root: %w", err)
	}
	var result Result
	result.Root = absRoot
	if reason := sensitiveAncestorReason(absRoot); reason != "" {
		result.Ignored = append(result.Ignored, Ignored{Path: ".", Reason: reason})
		return result, nil
	}
	ignoredByGit, err := newIgnoreMatcher(absRoot)
	if err != nil {
		return Result{}, err
	}
	outputPath := cfg.Output.Dir
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(absRoot, outputPath)
	}
	outputPath = filepath.Clean(outputPath)

	err = filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Ignored = append(result.Ignored, Ignored{Path: rel(absRoot, path), Reason: walkErr.Error()})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == absRoot {
			return nil
		}
		relPath := rel(absRoot, path)
		if visit != nil {
			visit(filepath.ToSlash(relPath), len(result.Files))
		}
		if entry.IsDir() && filepath.Clean(path) == outputPath {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath + "/", Reason: "configured output directory"})
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "symbolic link"})
			return nil
		}
		if entry.IsDir() {
			if reason := ignoreDir(relPath, entry.Name()); reason != "" {
				result.Ignored = append(result.Ignored, Ignored{Path: relPath + "/", Reason: reason})
				return filepath.SkipDir
			}
			if ignoredByGit.ignored(relPath, true) {
				result.Ignored = append(result.Ignored, Ignored{Path: relPath + "/", Reason: "gitignored"})
				return filepath.SkipDir
			}
			if err := ignoredByGit.loadDir(relPath); err != nil {
				return err
			}
			return nil
		}
		reason := ignoreFile(relPath, entry.Name())
		if reason != "" {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: reason})
			return nil
		}
		if ignoredByGit.ignored(relPath, false) {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "gitignored"})
			return nil
		}
		lang := LanguageForPath(relPath)
		if lang == "" {
			lang = languageFromShebang(path)
		}
		if lang == "" {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "unsupported file type"})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: err.Error()})
			return nil
		}
		if info.Size() > cfg.Scan.MaxFileSizeBytes {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "over max file size"})
			return nil
		}
		if result.TotalBytes+info.Size() > cfg.Scan.MaxTotalBytes {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "over max total size"})
			return nil
		}
		hash, err := fileHash(path)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "permission denied"})
				return nil
			}
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: err.Error()})
			return nil
		}
		result.Files = append(result.Files, File{
			Path:     filepath.ToSlash(relPath),
			AbsPath:  path,
			Language: lang,
			Size:     info.Size(),
			Hash:     hash,
			ModTime:  info.ModTime().UTC(),
		})
		result.TotalBytes += info.Size()
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	sort.Slice(result.Ignored, func(i, j int) bool { return result.Ignored[i].Path < result.Ignored[j].Path })
	return result, nil
}

func languageFromShebang(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	line := string(buf[:n])
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(fields) == 0 {
		return ""
	}
	interpreter := filepath.Base(fields[0])
	if interpreter == "env" {
		for index := 1; index < len(fields); index++ {
			field := fields[index]
			if field == "--" {
				continue
			}
			if field == "-u" || field == "--unset" || field == "-C" || field == "--chdir" || field == "-P" || field == "--path" {
				index++
				continue
			}
			if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
				continue
			}
			interpreter = filepath.Base(field)
			break
		}
	}
	interpreter = strings.Trim(interpreter, `"'`)
	switch {
	case strings.HasPrefix(interpreter, "python"):
		return "python"
	case interpreter == "node", interpreter == "nodejs", interpreter == "deno", interpreter == "bun":
		return "javascript"
	case interpreter == "sh", interpreter == "bash", interpreter == "zsh", interpreter == "dash", interpreter == "ksh", interpreter == "fish":
		return "shell"
	case interpreter == "ruby":
		return "ruby"
	case interpreter == "php":
		return "php"
	case interpreter == "perl":
		return "perl"
	case interpreter == "pwsh", interpreter == "powershell":
		return "powershell"
	case interpreter == "lua", interpreter == "luajit":
		return "lua"
	case interpreter == "elixir":
		return "elixir"
	default:
		return ""
	}
}

func LanguageForPath(path string) string {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs", ".ejs":
		return "javascript"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".swift":
		return "swift"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala", ".sc":
		return "scala"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".hpp", ".hh":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".fs", ".fsx":
		return "fsharp"
	case ".vb":
		return "visual-basic"
	case ".dart":
		return "dart"
	case ".ex", ".exs":
		return "elixir"
	case ".erl", ".hrl":
		return "erlang"
	case ".clj", ".cljs", ".cljc":
		return "clojure"
	case ".lua", ".luau":
		return "lua"
	case ".r":
		return "r"
	case ".m", ".mm":
		return "objective-c"
	case ".pl", ".pm":
		return "perl"
	case ".groovy", ".gradle":
		return "groovy"
	case ".sol":
		return "solidity"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".ps1", ".psm1":
		return "powershell"
	case ".sql":
		return "sql"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".html", ".htm":
		return "html"
	case ".vue":
		return "vue"
	case ".astro":
		return "astro"
	case ".svelte":
		return "svelte"
	case ".css", ".scss", ".sass":
		return "css"
	case ".md", ".mdx":
		return "markdown"
	case ".rst", ".adoc", ".asciidoc":
		return "documentation"
	case ".txt":
		return "text"
	case ".pdf":
		return "pdf"
	case ".docx", ".odt", ".rtf":
		return "document"
	case ".tf", ".tfvars", ".hcl":
		return "terraform"
	case ".proto":
		return "protobuf"
	case ".graphql", ".gql":
		return "graphql"
	case ".mod", ".sum":
		return "go"
	}
	switch base {
	case "Dockerfile":
		return "dockerfile"
	case "Makefile":
		return "make"
	case "Brewfile":
		return "ruby"
	}
	if strings.HasSuffix(strings.ToLower(base), ".podspec") {
		return "ruby"
	}
	return ""
}

func ignoreDir(path, name string) string {
	if isSensitiveDirectoryName(name) {
		return "sensitive credential directory"
	}
	switch name {
	case ".git", "node_modules", "vendor", "Pods", "DerivedData", ".build", "dist", "coverage", ".next", ".nuxt", "target", ".idea", ".vscode", ".reporavel", "ravel-graph",
		"venv", ".venv", "env", ".env", "__pycache__", "out", "site-packages", "lib64", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".eggs",
		"graphify-out", ".graphify", "lcov-report", "visual-tests", "visual-test", "__snapshots__", "storybook-static", "dist-protected",
		".turbo", ".angular", ".cache", ".parcel-cache", ".svelte-kit", ".terraform", ".serverless", ".worktrees":
		return "default ignored directory"
	}
	if strings.HasSuffix(name, "_venv") || strings.HasSuffix(name, "_env") || strings.HasSuffix(name, ".egg-info") {
		return "default ignored directory"
	}
	if name == "worktrees" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".") {
		return "default ignored directory"
	}
	if strings.HasPrefix(name, ".") && (name == ".cache" || strings.HasSuffix(name, "_cache")) {
		return "cache directory"
	}
	return ""
}

func ignoreFile(path, name string) string {
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return "secret-like environment file"
	}
	switch {
	case defaultIgnoredFile(lower):
		return "generated dependency lockfile"
	case strings.HasSuffix(lower, ".pem"), strings.HasSuffix(lower, ".key"), strings.HasSuffix(lower, ".p8"), strings.HasSuffix(lower, ".der"), strings.HasSuffix(lower, ".crt"), strings.HasSuffix(lower, ".cer"), strings.HasSuffix(lower, ".p12"), strings.HasSuffix(lower, ".pfx"), strings.HasSuffix(lower, ".jks"), strings.HasSuffix(lower, ".keystore"):
		return "secret-like key material"
	case isSensitiveFilename(name):
		return "sensitive credential filename"
	case strings.HasSuffix(lower, ".min.js"):
		return "minified generated file"
	case strings.HasSuffix(lower, ".lock"):
		return "lock file"
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".webp"), strings.HasSuffix(lower, ".ico"):
		return "binary image"
	case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".mov"), strings.HasSuffix(lower, ".avi"):
		return "binary video"
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".tgz"):
		return "archive"
	case strings.HasSuffix(lower, ".sqlite"), strings.HasSuffix(lower, ".db"):
		return "database file"
	}
	return ""
}

func defaultIgnoredFile(lower string) bool {
	switch lower {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "poetry.lock", "gemfile.lock", "composer.lock", "go.sum", "go.work.sum":
		return true
	default:
		return false
	}
}

func sensitiveAncestorReason(path string) string {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if isSensitiveDirectoryName(filepath.Base(current)) {
			return "inside sensitive credential directory"
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
	}
}

func isSensitiveDirectoryName(name string) bool {
	switch strings.ToLower(name) {
	case ".ssh", ".aws", ".gcloud", "secrets", "credentials":
		return true
	default:
		return false
	}
}

func isSensitiveFilename(name string) bool {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for i, word := range words {
		switch word {
		case "credential", "credentials", "secret", "secrets", "apitoken", "accesstoken", "privatekey":
			return true
		}
		if i+1 >= len(words) {
			continue
		}
		next := words[i+1]
		if ((word == "api" || word == "access") && next == "token") || (word == "private" && next == "key") {
			return true
		}
	}
	return false
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func rel(root, path string) string {
	out, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(out)
}
