//revive:disable:exported package-comments
package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const MaxFileBytes = int64(1 << 20)

type File struct {
	Plugins map[string]Plugin `yaml:"plugins"`
}

type Plugin struct {
	Description    string   `yaml:"description"`
	Scopes         []string `yaml:"scopes"`
	ShortCut       string   `yaml:"shortCut"`
	Override       bool     `yaml:"override"`
	Command        string   `yaml:"command"`
	Args           []string `yaml:"args"`
	Background     bool     `yaml:"background"`
	Confirm        bool     `yaml:"confirm"`
	Mutates        bool     `yaml:"mutates"`
	PreviewCommand string   `yaml:"previewCommand"`
	PreviewArgs    []string `yaml:"previewArgs"`
}

type CommandPlan struct {
	Command        string
	Args           []string
	Background     bool
	Confirm        bool
	Mutates        bool
	PreviewCommand string
	PreviewArgs    []string
}

func Load(path string) (File, error) {
	var file File
	input, err := os.Open(path) // #nosec G304 -- plugin file path is intentionally user-selectable.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return file, err
	}
	defer func() { _ = input.Close() }()
	data, err := io.ReadAll(io.LimitReader(input, MaxFileBytes+1))
	if err != nil {
		return file, err
	}
	if int64(len(data)) > MaxFileBytes {
		return file, fmt.Errorf("%s: plugin file exceeds %d bytes", path, MaxFileBytes)
	}
	return file, yaml.Unmarshal(data, &file)
}

func LoadMerged(paths ...string) (File, error) {
	merged := File{Plugins: map[string]Plugin{}}
	for _, path := range paths {
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
	previewArgs := make([]string, len(plugin.PreviewArgs))
	for i, arg := range plugin.PreviewArgs {
		previewArgs[i] = expand(arg, vars)
	}
	return CommandPlan{
		// The executable is trusted configuration. Expanding cluster-derived
		// values here would allow a resource name to select a PATH executable.
		Command:        plugin.Command,
		Args:           args,
		Background:     plugin.Background,
		Confirm:        plugin.Confirm,
		Mutates:        plugin.Mutates,
		PreviewCommand: plugin.PreviewCommand,
		PreviewArgs:    previewArgs,
	}
}

func Preview(ctx context.Context, plan CommandPlan, maxBytes int) (string, error) {
	if !plan.Mutates {
		return "Plugin is declared read-only.", nil
	}
	if strings.TrimSpace(plan.PreviewCommand) == "" {
		return "", errors.New("mutating plugin requires previewCommand and previewArgs")
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	output := &boundedOutput{limit: maxBytes}
	cmd := exec.CommandContext(ctx, plan.PreviewCommand, plan.PreviewArgs...) // #nosec G204 -- preview command is explicit trusted configuration.
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	text := strings.ToValidUTF8(output.String(), "�")
	if output.Truncated() {
		text += "\n… preview truncated"
	}
	if err != nil {
		return "", fmt.Errorf("plugin preview failed: %w: %s", err, text)
	}
	return text, nil
}

type boundedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:min(len(p), remaining)])
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *boundedOutput) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
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
