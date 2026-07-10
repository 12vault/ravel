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
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.Contains(line, " ") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch section + "." + key {
		case ".version":
			if v, err := strconv.Atoi(value); err == nil {
				cfg.Version = v
			}
		case ".mode":
			cfg.Mode = value
		case "scan.maxFileSize", "scan.maxFileSizeBytes":
			if v, err := strconv.ParseInt(value, 10, 64); err == nil {
				cfg.Scan.MaxFileSizeBytes = v
			}
		case "scan.maxTotalSize", "scan.maxTotalBytes":
			if v, err := strconv.ParseInt(value, 10, 64); err == nil {
				cfg.Scan.MaxTotalBytes = v
			}
		case "analysis.callGraph":
			cfg.Analysis.CallGraph = parseBool(value, cfg.Analysis.CallGraph)
		case "analysis.typeResolution":
			cfg.Analysis.TypeResolution = parseBool(value, cfg.Analysis.TypeResolution)
		case "output.dir":
			if value != "" {
				cfg.Output.Dir = value
			}
		case "output.json":
			cfg.Output.JSON = parseBool(value, cfg.Output.JSON)
		case "output.sqlite":
			cfg.Output.SQLite = parseBool(value, cfg.Output.SQLite)
		case "output.markdownReport":
			cfg.Output.MarkdownReport = parseBool(value, cfg.Output.MarkdownReport)
		}
	}
	return cfg, nil
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

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	case "false", "no", "0":
		return false
	default:
		return fallback
	}
}
