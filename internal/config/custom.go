package config

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Hotkey struct {
	ShortCut    string `yaml:"shortCut"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
}
type View struct {
	Columns    []string `yaml:"columns"`
	SortColumn string   `yaml:"sortColumn"`
}
type Custom struct {
	Aliases map[string]string
	Hotkeys map[string]Hotkey
	Views   map[string]View
}

func LoadCustom(dir string) (Custom, error) {
	c := Custom{}
	inputs := []struct {
		name  string
		value any
	}{
		{"aliases.yaml", &struct {
			Aliases *map[string]string `yaml:"aliases"`
		}{&c.Aliases}},
		{"hotkeys.yaml", &struct {
			Hotkeys *map[string]Hotkey `yaml:"hotKeys"`
		}{&c.Hotkeys}},
		{"views.yaml", &struct {
			Views *map[string]View `yaml:"views"`
		}{&c.Views}},
	}
	for _, input := range inputs {
		data, err := os.ReadFile(filepath.Join(dir, input.name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return c, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err = dec.Decode(input.value); err != nil {
			return c, fmt.Errorf("%s: %w", input.name, err)
		}
	}
	for name := range c.Aliases {
		if _, err := c.Expand(name); err != nil {
			return c, err
		}
	}
	for name, h := range c.Hotkeys {
		if strings.TrimSpace(h.ShortCut) == "" || strings.TrimSpace(h.Command) == "" {
			return c, fmt.Errorf("hotkey %s requires shortCut and command", name)
		}
		switch strings.ToLower(h.ShortCut) {
		case "ctrl-a", "ctrl+a", "ctrl-s", "ctrl+s", "ctrl-k", "ctrl+k", "space", "ctrl-p", "ctrl+p", "ctrl-d", "ctrl+d", "ctrl-e", "ctrl+e", "?", ":", "/", "q", "e", "d", "y", "x", "l", "f", "s", "r", "w", "b", "0":
			return c, fmt.Errorf("hotkey %s conflicts with built-in key %s", name, h.ShortCut)
		}
	}
	for name, v := range c.Views {
		if _, pattern, ok := strings.Cut(name, "@"); ok {
			if _, err := regexp.Compile(pattern); err != nil {
				return c, fmt.Errorf("views %q: %w", name, err)
			}
		}
		for _, col := range v.Columns {
			_, attrs, _ := strings.Cut(col, "|")
			for _, r := range attrs {
				if !strings.ContainsRune("TNWSHLR", r) {
					return c, fmt.Errorf("view %s: unsupported attribute %c", name, r)
				}
			}
		}
	}
	return c, nil
}
func (c Custom) Expand(command string) (string, error) {
	seen := map[string]bool{}
	for {
		parts := strings.Fields(strings.TrimPrefix(command, ":"))
		if len(parts) == 0 {
			return command, nil
		}
		alias, ok := c.Aliases[parts[0]]
		if !ok {
			return command, nil
		}
		if seen[parts[0]] {
			return "", fmt.Errorf("alias cycle at %s", parts[0])
		}
		seen[parts[0]] = true
		command = strings.TrimSpace(alias + " " + strings.Join(parts[1:], " "))
	}
}
