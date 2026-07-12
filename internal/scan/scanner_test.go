package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/config"
)

func TestLanguageForPathRecognizesPopularAgentLanguages(t *testing.T) {
	tests := map[string]string{
		"app.py":       "python",
		"app.tsx":      "typescript",
		"app.mts":      "typescript",
		"app.mjs":      "javascript",
		"plugin.cjs":   "javascript",
		"site.astro":   "astro",
		"Pod.podspec":  "ruby",
		"build.gradle": "groovy",
		"main.rs":      "rust",
		"App.vue":      "vue",
		"main.dart":    "dart",
		"schema.proto": "protobuf",
		"infra.tf":     "terraform",
		"query.gql":    "graphql",
		"paper.pdf":    "pdf",
		"guide.docx":   "document",
	}
	for path, want := range tests {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestScanRejectsUnsupportedFilesAndAdmitsSupportedShebangs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "program.wren"), []byte("class App {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blob.custom"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tool"), []byte("#!/usr/bin/env python3\nprint('ok')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "tool" || result.Files[0].Language != "python" {
		t.Fatalf("files = %#v", result.Files)
	}
	if len(result.Ignored) != 2 || result.Ignored[0].Reason != "unsupported file type" || result.Ignored[1].Reason != "unsupported file type" {
		t.Fatalf("ignored = %#v", result.Ignored)
	}
}

func TestScanUsesGraphifyCompatibleNoiseExclusions(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node_modules", ".venv", "__pycache__", ".turbo", ".svelte-kit", "storybook-static", "pkg.egg-info"} {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "generated.py"), []byte("def generated(): pass\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"package-lock.json", "pnpm-lock.yaml", "go.sum"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("generated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("def main(): pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "main.py" {
		t.Fatalf("files = %#v, ignored = %#v", result.Files, result.Ignored)
	}
}

func TestScanRecognizesEnvShebangOptions(t *testing.T) {
	root := t.TempDir()
	for name, shebang := range map[string]string{
		"split":  "#!/usr/bin/env -S python3 -u\n",
		"unset":  "#!/usr/bin/env -u DEBUG node\n",
		"assign": "#!/usr/bin/env DEBUG=1 bash\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(shebang), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	languages := map[string]string{}
	for _, file := range result.Files {
		languages[file.Path] = file.Language
	}
	for path, want := range map[string]string{"split": "python", "unset": "javascript", "assign": "shell"} {
		if got := languages[path]; got != want {
			t.Errorf("language for %s = %q, want %q", path, got, want)
		}
	}
}

func TestScanDoesNotMistakeSourceDirectoryNamedBuildForGeneratedOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "build", "build.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "internal/build/build.go" {
		t.Fatalf("source build directory was pruned: files = %#v, ignored = %#v", result.Files, result.Ignored)
	}
}

func TestScanExcludesConfiguredOutputDirectory(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "custom-ravel-output")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "cached.go"), []byte("package generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Output.Dir = "custom-ravel-output"
	result, err := Scan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "main.go" {
		t.Fatalf("scanned files = %#v, want only main.go", result.Files)
	}
	ignoredOutput := false
	for _, ignored := range result.Ignored {
		if ignored.Path == "custom-ravel-output/" && ignored.Reason == "configured output directory" {
			ignoredOutput = true
		}
	}
	if !ignoredOutput {
		t.Fatalf("configured output directory was not reported as ignored: %#v", result.Ignored)
	}
}

func TestScanRejectsSymlinksBeforeReadingTargets(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package leaked\nfunc SecretOutsideRoot() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	link := filepath.Join(root, "safe.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("symlink target escaped audited root: %#v", result.Files)
	}
	if len(result.Ignored) != 1 || result.Ignored[0].Path != "safe.go" || result.Ignored[0].Reason != "symbolic link" {
		t.Fatalf("ignored symlink = %#v", result.Ignored)
	}
}

func TestScanNeverLoadsSymlinkedGitignore(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "root"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			outside := filepath.Join(t.TempDir(), "outside-ignore")
			if err := os.WriteFile(outside, []byte("ignored-by-outside.go\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			directory := root
			relativeFile := "ignored-by-outside.go"
			if nested {
				directory = filepath.Join(root, "nested")
				relativeFile = "nested/ignored-by-outside.go"
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "ignored-by-outside.go"), []byte("package safe\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(directory, ".gitignore")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			result, err := Scan(root, config.Default())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range result.Files {
				if file.Path == relativeFile {
					found = true
				}
			}
			if !found {
				t.Fatalf("symlink target supplied ignore rules: files=%#v ignored=%#v", result.Files, result.Ignored)
			}
			ignorePath := ".gitignore"
			if nested {
				ignorePath = "nested/.gitignore"
			}
			ignoredLink := false
			for _, ignored := range result.Ignored {
				if ignored.Path == ignorePath && ignored.Reason == "symbolic link" {
					ignoredLink = true
				}
			}
			if !ignoredLink {
				t.Fatalf("symlinked gitignore not reported: %#v", result.Ignored)
			}
		})
	}
}

func TestScanCanonicalizesSymlinkedRootBeforeSensitiveAncestorCheck(t *testing.T) {
	parent := t.TempDir()
	sensitive := filepath.Join(parent, ".ssh", "fixture")
	if err := os.MkdirAll(sensitive, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "repository")
	if err := os.Symlink(sensitive, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := Scan(link, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 || len(result.Ignored) != 1 || result.Ignored[0].Reason != "inside sensitive credential directory" {
		t.Fatalf("symlinked sensitive root was not rejected: %#v", result)
	}
}

func TestScanRejectsSecretKeyMaterialAndSensitiveFilenames(t *testing.T) {
	root := t.TempDir()
	keyMaterial := []string{
		"signing.pem",
		"signing.key",
		"signing.p8",
		"signing.der",
		"signing.crt",
		"signing.cer",
		"signing.p12",
		"signing.pfx",
		"signing.jks",
		"signing.keystore",
	}
	sensitiveNames := []string{
		".ENV.production",
		"credentials.json",
		"aws_credentials.yaml",
		"secrets.toml",
		"api_token.txt",
		"access-token.json",
		"private.key.txt",
		"accessToken.txt",
	}
	for _, name := range append(keyMaterial, sensitiveNames...) {
		if err := os.WriteFile(filepath.Join(root, name), []byte("test fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"key.go", "keyboard.ts", "monkey_patch.py", "tokenizer.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("test fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}

	var included []string
	for _, file := range result.Files {
		included = append(included, file.Path)
	}
	sort.Strings(included)
	wantIncluded := []string{"key.go", "keyboard.ts", "monkey_patch.py", "tokenizer.go"}
	if len(included) != len(wantIncluded) {
		t.Fatalf("included files = %q, want %q", included, wantIncluded)
	}
	for i := range wantIncluded {
		if included[i] != wantIncluded[i] {
			t.Fatalf("included files = %q, want %q", included, wantIncluded)
		}
	}

	reasons := make(map[string]string, len(result.Ignored))
	for _, ignored := range result.Ignored {
		reasons[ignored.Path] = ignored.Reason
	}
	for _, name := range keyMaterial {
		if got := reasons[name]; got != "secret-like key material" {
			t.Errorf("ignored reason for %q = %q, want secret-like key material", name, got)
		}
	}
	for _, name := range sensitiveNames {
		want := "sensitive credential filename"
		if strings.HasPrefix(strings.ToLower(name), ".env.") {
			want = "secret-like environment file"
		}
		if got := reasons[name]; got != want {
			t.Errorf("ignored reason for %q = %q, want %q", name, got, want)
		}
	}
}

func TestScanPrunesSensitiveCredentialDirectories(t *testing.T) {
	root := t.TempDir()
	directories := []string{".ssh", ".aws", ".gcloud", "secrets", "credentials"}
	for _, name := range directories {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "must-not-be-scanned.go"), []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("files from sensitive directories were scanned: %#v", result.Files)
	}
	if len(result.Ignored) != len(directories) {
		t.Fatalf("ignored = %#v, want one entry per pruned directory", result.Ignored)
	}
	for _, ignored := range result.Ignored {
		if ignored.Reason != "sensitive credential directory" {
			t.Errorf("ignored reason for %q = %q, want sensitive credential directory", ignored.Path, ignored.Reason)
		}
	}
	if got := ignoreDir("CREDENTIALS", "CREDENTIALS"); got != "sensitive credential directory" {
		t.Errorf("case-insensitive ignored reason = %q, want sensitive credential directory", got)
	}
}

func TestScanRejectsRootInsideSensitiveCredentialDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".ssh", "fixture")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "known_hosts"), []byte("test fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("files = %#v, want no files from sensitive parent directory", result.Files)
	}
	if len(result.Ignored) != 1 || result.Ignored[0].Path != "." || result.Ignored[0].Reason != "inside sensitive credential directory" {
		t.Fatalf("ignored = %#v", result.Ignored)
	}
}

func TestScanHonorsRootAndNestedGitignoreBeforeReadingFiles(t *testing.T) {
	root := t.TempDir()
	rootIgnore := `ignored.txt
logs/
*.local
!keep.local
/root-only.txt
`
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(rootIgnore), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ignored.txt", "drop.local", "keep.local", "root-only.txt", "visible.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "logs", "unreadable.go"), []byte("package ignored\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", ".gitignore"), []byte("draft-*.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"draft-one.txt", "public.txt"} {
		if err := os.WriteFile(filepath.Join(root, "nested", name), []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	var included []string
	for _, file := range result.Files {
		included = append(included, file.Path)
	}
	sort.Strings(included)
	want := []string{"nested/public.txt", "visible.go"}
	if strings.Join(included, "\n") != strings.Join(want, "\n") {
		t.Fatalf("included = %q, want %q", included, want)
	}
	ignored := map[string]string{}
	for _, item := range result.Ignored {
		ignored[item.Path] = item.Reason
	}
	for _, path := range []string{"ignored.txt", "drop.local", "root-only.txt", "logs/", "nested/draft-one.txt"} {
		if ignored[path] != "gitignored" {
			t.Errorf("ignored[%q] = %q, want gitignored", path, ignored[path])
		}
	}
}

func TestScanMergesNestedRavelignoreAfterGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("shared.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ravelignore"), []byte("!shared.go\nravel-only.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shared.go", "ravel-only.go", "visible.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".ravelignore"), []byte("hidden.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "hidden.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	var included []string
	for _, file := range result.Files {
		included = append(included, file.Path)
	}
	want := []string{"shared.go", "visible.go"}
	if strings.Join(included, "\n") != strings.Join(want, "\n") {
		t.Fatalf("included = %q, want %q; ignored = %#v", included, want, result.Ignored)
	}
}

func TestGitignoreSupportsDoubleStarAndCharacterClasses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("artifacts/**/cache-[0-9].json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifacts", "a", "b")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cache-1.json", "cache-x.json"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		if file.Path == "artifacts/a/b/cache-1.json" {
			t.Fatal("double-star ignored file was scanned")
		}
	}
}

func TestGitignoreEscapedLeadingBangIsLiteral(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("\\!literal.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"!literal.txt", "literal.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	included := map[string]bool{}
	for _, file := range result.Files {
		included[file.Path] = true
	}
	if included["!literal.txt"] {
		t.Fatal("escaped leading bang was treated as negation instead of a literal pattern")
	}
	if !included["literal.txt"] {
		t.Fatal("literal pattern unexpectedly ignored the unprefixed filename")
	}
}
