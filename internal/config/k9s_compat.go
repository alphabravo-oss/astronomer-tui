//revive:disable:exported package-comments
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxCompatibilityFileBytes = int64(1 << 20)
	MaxK9sAliases             = 500
	MaxK9sHotKeys             = 200
)

var shortcutPattern = regexp.MustCompile(`(?i)^(?:ctrl|alt|shift)-[a-z0-9]$`)
var aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type AliasFile struct {
	Aliases map[string]string `yaml:"aliases"`
}

type HotKeyFile struct {
	HotKeys map[string]HotKey `yaml:"hotKeys"`
}

type HotKey struct {
	ShortCut    string `yaml:"shortCut"`
	Override    bool   `yaml:"override"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
	KeepHistory bool   `yaml:"keepHistory"`
}

func DefaultAliasesPath() string { return filepath.Join(DefaultDir(), "aliases.yaml") }

func DefaultHotKeysPath() string { return filepath.Join(DefaultDir(), "hotkeys.yaml") }

func ImportedK9sConfigPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "config.k9s.yaml")
}

func LoadAliases(path string) (map[string]string, error) {
	var file AliasFile
	if err := decodeBoundedYAML(path, &file, true); err != nil {
		return nil, err
	}
	if len(file.Aliases) > MaxK9sAliases {
		return nil, fmt.Errorf("%s: aliases exceeds %d entries", path, MaxK9sAliases)
	}
	aliases := make(map[string]string, len(file.Aliases))
	for rawAlias, rawTarget := range file.Aliases {
		alias := strings.ToLower(strings.TrimSpace(rawAlias))
		target := strings.TrimSpace(rawTarget)
		if !aliasPattern.MatchString(alias) {
			return nil, fmt.Errorf("%s: invalid alias %q", path, rawAlias)
		}
		if target == "" || len(target) > 256 || strings.ContainsAny(target, "\r\n\x00") || !utf8.ValidString(target) {
			return nil, fmt.Errorf("%s: invalid target for alias %q", path, rawAlias)
		}
		aliases[alias] = target
	}
	return aliases, nil
}

func LoadHotKeys(path string) (map[string]HotKey, error) {
	var file HotKeyFile
	if err := decodeBoundedYAML(path, &file, true); err != nil {
		return nil, err
	}
	if len(file.HotKeys) > MaxK9sHotKeys {
		return nil, fmt.Errorf("%s: hotKeys exceeds %d entries", path, MaxK9sHotKeys)
	}
	hotkeys := make(map[string]HotKey, len(file.HotKeys))
	names := make([]string, 0, len(file.HotKeys))
	for name := range file.HotKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		hotkey := file.HotKeys[name]
		shortcut, err := CanonicalShortcut(hotkey.ShortCut)
		if err != nil {
			return nil, fmt.Errorf("%s: hotKeys.%s.shortCut: %w", path, name, err)
		}
		hotkey.Command = strings.TrimSpace(hotkey.Command)
		if hotkey.Command == "" || len(hotkey.Command) > 256 || strings.ContainsAny(hotkey.Command, "\r\n\x00") || !utf8.ValidString(hotkey.Command) {
			return nil, fmt.Errorf("%s: hotKeys.%s.command is invalid", path, name)
		}
		if len(hotkey.Description) > 256 || !validDisplayString(hotkey.Description) {
			return nil, fmt.Errorf("%s: hotKeys.%s.description is invalid", path, name)
		}
		if _, exists := hotkeys[shortcut]; exists {
			return nil, fmt.Errorf("%s: duplicate shortcut %s", path, shortcut)
		}
		hotkey.ShortCut = shortcut
		hotkeys[shortcut] = hotkey
	}
	return hotkeys, nil
}

func validDisplayString(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func CanonicalShortcut(input string) (string, error) {
	input = strings.TrimSpace(input)
	if utf8.RuneCountInString(input) == 1 {
		r, _ := utf8.DecodeRuneInString(input)
		if r < 0x21 || r > 0x7e {
			return "", errors.New("shortcut must be a printable ASCII key")
		}
		return strings.ToLower(string(r)), nil
	}
	if !shortcutPattern.MatchString(input) {
		return "", errors.New("shortcut must be one key or Ctrl-, Alt-, or Shift- plus a letter or digit")
	}
	parts := strings.SplitN(strings.ToLower(input), "-", 2)
	return strings.ToUpper(parts[0][:1]) + parts[0][1:] + "-" + strings.ToUpper(parts[1]), nil
}

func ApplyImportedK9sReadOnly(cfg Config, path string) (Config, error) {
	var imported struct {
		K9s struct {
			ReadOnly bool `yaml:"readOnly"`
		} `yaml:"k9s"`
	}
	if err := decodeBoundedYAML(path, &imported, false); err != nil {
		return cfg, err
	}
	if imported.K9s.ReadOnly && cfg.Safety.DefaultPolicy == "" {
		cfg.Safety.DefaultPolicy = "read-only"
	}
	return cfg, nil
}

func decodeBoundedYAML(path string, target any, knownFields bool) error {
	file, err := os.Open(path) // #nosec G304 -- compatibility path is explicit and user-owned.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, MaxCompatibilityFileBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxCompatibilityFileBytes {
		return fmt.Errorf("%s: compatibility file exceeds %d bytes", path, MaxCompatibilityFileBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(knownFields)
	if err := decoder.Decode(target); err != nil {
		return validationError(path, err)
	}
	return nil
}
