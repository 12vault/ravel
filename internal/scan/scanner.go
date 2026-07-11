package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/12ya/reporavel/internal/config"
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
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	var result Result
	result.Root = absRoot

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
		if entry.IsDir() {
			if reason := ignoreDir(relPath, entry.Name()); reason != "" {
				result.Ignored = append(result.Ignored, Ignored{Path: relPath + "/", Reason: reason})
				return filepath.SkipDir
			}
			return nil
		}
		reason := ignoreFile(relPath, entry.Name())
		if reason != "" {
			result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: reason})
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
		lang := LanguageForPath(relPath)
		if lang == "" {
			if !isTextFile(path) {
				result.Ignored = append(result.Ignored, Ignored{Path: relPath, Reason: "binary or unsupported file type"})
				return nil
			}
			lang = "unknown"
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

func isTextFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	buf = buf[:n]
	return !strings.ContainsRune(string(buf), '\x00') && utf8.Valid(buf)
}

func LanguageForPath(path string) string {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
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
	case ".groovy":
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
	}
	return ""
}

func ignoreDir(path, name string) string {
	switch name {
	case ".git", "node_modules", "vendor", "Pods", "DerivedData", ".build", "dist", "build", "coverage", ".next", ".nuxt", "target", ".idea", ".vscode", ".reporavel", "ravel-graph":
		return "default ignored directory"
	}
	if strings.HasPrefix(name, ".") && (name == ".cache" || strings.HasSuffix(name, "_cache")) {
		return "cache directory"
	}
	return ""
}

func ignoreFile(path, name string) string {
	lower := strings.ToLower(name)
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return "secret-like environment file"
	}
	switch {
	case strings.HasSuffix(lower, ".pem"), strings.HasSuffix(lower, ".key"), strings.HasSuffix(lower, ".p12"), strings.HasSuffix(lower, ".pfx"):
		return "secret-like key material"
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
