//revive:disable:exported package-comments
package plugins

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Plugins map[string]Plugin `yaml:"plugins"`
}

type Plugin struct {
	Description string   `yaml:"description"`
	Scopes      []string `yaml:"scopes"`
	ShortCut    string   `yaml:"shortCut"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	Background  bool     `yaml:"background"`
	Confirm     bool     `yaml:"confirm"`
}

type CommandPlan struct {
	Command    string
	Args       []string
	Background bool
	Confirm    bool
}

func Load(path string) (File, error) {
	var file File
	data, err := os.ReadFile(path) // #nosec G304 -- plugin file path is intentionally user-selectable.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return file, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err == nil && file.Plugins != nil {
		return file, nil
	}
	var single Plugin
	dec = yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&single); err == nil && single.Command != "" {
		return File{Plugins: map[string]Plugin{strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)): single}}, nil
	}
	return file, fmt.Errorf("invalid or unsupported plugin fields in %s", path)
}

func LoadMerged(paths ...string) (File, error) {
	merged := File{Plugins: map[string]Plugin{}}
	var expanded []string
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return merged, err
			}
			for _, entry := range entries {
				if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
					expanded = append(expanded, filepath.Join(path, entry.Name()))
				}
			}
		} else {
			expanded = append(expanded, path)
		}
	}
	for _, path := range expanded {
		file, err := Load(path)
		if err != nil {
			return merged, err
		}
		for name, plugin := range file.Plugins {
			merged.Plugins[name] = plugin
		}
	}
	return merged, nil
}

func Plan(plugin Plugin, vars map[string]string) CommandPlan {
	args := make([]string, len(plugin.Args))
	for i, arg := range plugin.Args {
		args[i] = expand(arg, vars)
	}
	return CommandPlan{
		Command:    expand(plugin.Command, vars),
		Args:       args,
		Background: plugin.Background,
		Confirm:    plugin.Confirm,
	}
}

func Command(plan CommandPlan) *exec.Cmd {
	return exec.Command(plan.Command, plan.Args...) // #nosec G204 -- plugin commands are explicit user configuration.
}

func expand(input string, vars map[string]string) string {
	out := input
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		value := vars[key]
		out = strings.ReplaceAll(out, "${"+key+"}", value)
		out = strings.ReplaceAll(out, "$"+key, value)
	}
	return out
}
