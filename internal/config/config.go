package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Version  int
	Mode     string
	Scan     ScanConfig
	Analysis AnalysisConfig
	Output   OutputConfig
}

type ScanConfig struct {
	MaxFileSizeBytes int64
	MaxTotalBytes    int64
}

type AnalysisConfig struct {
	Go             bool
	CallGraph      bool
	TypeResolution bool
}

type OutputConfig struct {
	Dir            string
	JSON           bool
	SQLite         bool
	MarkdownReport bool
}

func Default() Config {
	return Config{
		Version: 1,
		Mode:    "offline",
		Scan: ScanConfig{
			MaxFileSizeBytes: 1 << 20,
			MaxTotalBytes:    100 << 20,
		},
		Analysis: AnalysisConfig{
			Go:             true,
			CallGraph:      true,
			TypeResolution: false,
		},
		Output: OutputConfig{
			Dir:            ".reporavel",
			JSON:           true,
			SQLite:         false,
			MarkdownReport: true,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	section := ""
	for index, raw := range strings.Split(string(data), "\n") {
		lineNumber := index + 1
		if strings.Contains(raw, "\t") {
			return cfg, configError(path, lineNumber, "tabs are not supported")
		}
		line := strings.TrimSpace(stripInlineComment(raw))
		if line == "" {
			continue
		}
		indented := len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t')
		if strings.HasSuffix(line, ":") && !strings.Contains(strings.TrimSuffix(line, ":"), ":") {
			if indented {
				return cfg, configError(path, lineNumber, "nested sections are not supported")
			}
			section = strings.TrimSpace(strings.TrimSuffix(line, ":"))
			if !knownSection(section) {
				return cfg, configError(path, lineNumber, fmt.Sprintf("unknown section %q", section))
			}
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return cfg, configError(path, lineNumber, "expected key: value")
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))
		if key == "" || value == "" {
			return cfg, configError(path, lineNumber, "key and value must not be empty")
		}
		if !indented {
			section = ""
		}
		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}
		if err := applyValue(&cfg, fullKey, value); err != nil {
			return cfg, configError(path, lineNumber, err.Error())
		}
	}
	if err := validate(cfg); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func applyValue(cfg *Config, key, value string) error {
	switch key {
	case "version":
		parsed, err := parseInt(value)
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		cfg.Version = int(parsed)
	case "mode":
		cfg.Mode = value
	case "permissions.network", "permissions.shell", "permissions.llm", "permissions.subagents", "permissions.writeOutsideOutputDir", "permissions.readSecrets":
		enabled, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if enabled {
			return fmt.Errorf("%s cannot be enabled", key)
		}
	case "scan.maxFileSize", "scan.maxFileSizeBytes":
		parsed, err := parseInt(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Scan.MaxFileSizeBytes = parsed
	case "scan.maxTotalSize", "scan.maxTotalBytes":
		parsed, err := parseInt(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Scan.MaxTotalBytes = parsed
	case "analysis.go":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Analysis.Go = parsed
	case "analysis.callGraph":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Analysis.CallGraph = parsed
	case "analysis.typeResolution":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Analysis.TypeResolution = parsed
	case "output.dir":
		cfg.Output.Dir = value
	case "output.json":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Output.JSON = parsed
	case "output.sqlite":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Output.SQLite = parsed
	case "output.markdownReport":
		parsed, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Output.MarkdownReport = parsed
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

func validate(cfg Config) error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported version %d", cfg.Version)
	}
	if cfg.Mode != "offline" {
		return fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	if cfg.Scan.MaxFileSizeBytes <= 0 {
		return fmt.Errorf("scan.maxFileSize must be greater than zero")
	}
	if cfg.Scan.MaxTotalBytes <= 0 {
		return fmt.Errorf("scan.maxTotalSize must be greater than zero")
	}
	if cfg.Output.Dir == "" {
		return fmt.Errorf("output.dir must not be empty")
	}
	if cfg.Analysis.TypeResolution {
		return fmt.Errorf("analysis.typeResolution is not implemented")
	}
	if cfg.Output.SQLite {
		return fmt.Errorf("output.sqlite is not implemented")
	}
	if !cfg.Output.JSON && !cfg.Output.MarkdownReport {
		return fmt.Errorf("at least one output format must be enabled")
	}
	return nil
}

func knownSection(section string) bool {
	switch section {
	case "permissions", "scan", "analysis", "output":
		return true
	default:
		return false
	}
}

func parseInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("expected an integer, got %q", value)
	}
	return parsed, nil
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true, nil
	case "false", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", value)
	}
}

func stripInlineComment(line string) string {
	var quote rune
	for index, char := range line {
		switch {
		case quote == 0 && (char == '\'' || char == '"'):
			quote = char
		case quote == char:
			quote = 0
		case quote == 0 && char == '#':
			return line[:index]
		}
	}
	return line
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}

func configError(path string, line int, message string) error {
	return fmt.Errorf("config %s:%d: %s", path, line, message)
}

func DefaultYAML() string {
	return `version: 1

mode: offline

permissions:
  network: false
  shell: false
  llm: false
  subagents: false
  writeOutsideOutputDir: false
  readSecrets: false

scan:
  maxFileSize: 1048576
  maxTotalSize: 104857600

analysis:
  go: true
  callGraph: true
  typeResolution: false

output:
  dir: ".reporavel"
  json: true
  sqlite: false
  markdownReport: true
`
}

func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(DefaultYAML()), 0644)
}
