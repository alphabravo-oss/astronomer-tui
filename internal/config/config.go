//revive:disable:exported package-comments
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Safety     SafetyConfig             `yaml:"safety"`
	AI         AIConfig                 `yaml:"ai"`
	Kubernetes KubernetesConfig         `yaml:"kubernetes"`
	Contexts   map[string]ContextConfig `yaml:"contexts"`
	Theme      string                   `yaml:"theme"`
}

type KubernetesConfig struct {
	MaxCachedObjects     int `yaml:"maxCachedObjects"`
	MaxCachedObjectBytes int `yaml:"maxCachedObjectBytes"`
}

type SafetyConfig struct {
	ProdPattern string `yaml:"prodPattern"`
}

type AIConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"apiKeyEnv"`
	Endpoint  string `yaml:"endpoint"`
}

type ContextConfig struct {
	Policy string `yaml:"policy"`
}

type ValidationError struct {
	Path string
	Line int
	Col  int
	Err  error
}

func (v *ValidationError) Error() string {
	if v.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %v", v.Path, v.Line, v.Col, v.Err)
	}
	return fmt.Sprintf("%s: %v", v.Path, v.Err)
}

func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "astrotui")
	}
	return filepath.Join(home, ".config", "astrotui")
}

func DefaultPath() string {
	return filepath.Join(DefaultDir(), "config.yaml")
}

func DefaultK9sDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "k9s")
	}
	return filepath.Join(home, ".config", "k9s")
}

func Default() Config {
	return Config{
		Safety:     SafetyConfig{ProdPattern: `(?i)prod|production`},
		AI:         AIConfig{Enabled: false},
		Kubernetes: KubernetesConfig{MaxCachedObjects: 10_000, MaxCachedObjectBytes: 2 << 20},
		Theme:      "dark",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path) // #nosec G304 -- config path is intentionally user-selectable.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, validationError(path, err)
	}
	if cfg.Safety.ProdPattern == "" {
		cfg.Safety.ProdPattern = `(?i)prod|production`
	}
	if cfg.Kubernetes.MaxCachedObjects <= 0 {
		return cfg, &ValidationError{Path: path, Err: fmt.Errorf("kubernetes.maxCachedObjects must be greater than zero")}
	}
	if cfg.Kubernetes.MaxCachedObjectBytes <= 0 {
		return cfg, &ValidationError{Path: path, Err: fmt.Errorf("kubernetes.maxCachedObjectBytes must be greater than zero")}
	}
	if _, err := regexp.Compile(cfg.Safety.ProdPattern); err != nil {
		return cfg, &ValidationError{Path: path, Err: fmt.Errorf("safety.prodPattern is invalid: %w", err)}
	}
	if cfg.AI.Enabled {
		switch cfg.AI.Provider {
		case "anthropic", "openai", "ollama":
		default:
			return cfg, &ValidationError{Path: path, Err: fmt.Errorf("ai.provider must be anthropic, openai, or ollama")}
		}
		if err := ValidateAIEndpoint(cfg.AI.Endpoint); err != nil {
			return cfg, &ValidationError{Path: path, Err: err}
		}
	}
	for name, ctx := range cfg.Contexts {
		switch ctx.Policy {
		case "", "read-only", "confirm-destructive", "full":
		default:
			return cfg, &ValidationError{Path: path, Err: fmt.Errorf("contexts.%s.policy must be read-only, confirm-destructive, or full", name)}
		}
	}
	return cfg, nil
}

func ValidateAIEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("ai.endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("ai.endpoint must use HTTPS (HTTP is allowed only for loopback)")
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("ai.endpoint must use HTTPS for non-loopback hosts")
}

func validationError(path string, err error) error {
	line, col := yamlErrorPosition(err)
	return &ValidationError{Path: path, Line: line, Col: col, Err: err}
}

func yamlErrorPosition(err error) (int, int) {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		for _, msg := range typeErr.Errors {
			line, col := parseYAMLPosition(msg)
			if line > 0 {
				return line, col
			}
		}
	}
	return parseYAMLPosition(err.Error())
}

func parseYAMLPosition(msg string) (int, int) {
	// yaml.v3 formats many schema errors as "line N: field X not found ...".
	idx := strings.Index(msg, "line ")
	if idx < 0 {
		return 0, 0
	}
	rest := msg[idx+len("line "):]
	var line int
	if _, err := fmt.Sscanf(rest, "%d", &line); err != nil {
		return 0, 0
	}
	col := 1
	if colIdx := strings.Index(rest, "column "); colIdx >= 0 {
		_, _ = fmt.Sscanf(rest[colIdx+len("column "):], "%d", &col)
	}
	return line, col
}
