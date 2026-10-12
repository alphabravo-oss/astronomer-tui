//revive:disable:exported package-comments
package config

import (
	"errors"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
)

type ImportResult struct {
	File   string
	Status string
	Note   string
}

type importMapping struct {
	src string
	dst string
}

func ImportK9s(srcDir, dstDir string) ([]ImportResult, error) {
	mapping := []importMapping{
		{src: "config.yaml", dst: "config.k9s.yaml"},
		{src: "skin.yaml", dst: "skin.yaml"},
		{src: "hotkeys.yaml", dst: "hotkeys.yaml"},
		{src: "aliases.yaml", dst: "aliases.yaml"},
		{src: "views.yaml", dst: "views.yaml"},
		{src: "plugins.yaml", dst: "plugins.yaml"},
	}
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return nil, err
	}
	results := make([]ImportResult, 0, len(mapping))
	for _, file := range mapping {
		src := filepath.Join(srcDir, file.src)
		dst := filepath.Join(dstDir, file.dst)
		data, err := os.ReadFile(src) // #nosec G304 -- import source is an explicit user-selected directory.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				results = append(results, ImportResult{File: file.src, Status: "skipped"})
				continue
			}
			return nil, err
		}
		note := ""
		if file.src == "config.yaml" {
			note = "archived for reference; k9s global settings are not applied"
		}
		if file.src == "skin.yaml" {
			data, note = translateSkin(data)
		}
		if _, err := os.Stat(dst); err == nil {
			existing, readErr := os.ReadFile(dst) // #nosec G304 -- import destination is an explicit user-selected directory.
			if readErr != nil {
				return nil, readErr
			}
			status := "conflict"
			if string(existing) == string(data) {
				status = "skipped"
			}
			results = append(results, ImportResult{File: file.src, Status: status})
			continue
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil { // #nosec G304,G703 -- import destination is an explicit user-selected directory.
			return nil, err
		}
		results = append(results, ImportResult{File: file.src, Status: "imported", Note: note})
	}
	entries, _ := os.ReadDir(filepath.Join(srcDir, "plugins"))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, "plugins", name))
		if err != nil {
			return results, err
		}
		target := filepath.Join(dstDir, "plugins", name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return results, err
		}
		status := "imported"
		if old, e := os.ReadFile(target); e == nil {
			status = "conflict"
			if string(old) == string(data) {
				status = "skipped"
			}
		} else if err = os.WriteFile(target, data, 0600); err != nil {
			return results, err
		}
		results = append(results, ImportResult{File: "plugins/" + name, Status: status})
	}
	walkErr := filepath.WalkDir(filepath.Join(srcDir, "clusters"), func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		switch entry.Name() {
		case "aliases.yaml", "hotkeys.yaml", "views.yaml", "config.yaml", "benchmarks.yaml":
		default:
			return nil
		}
		relative, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		status := "imported"
		if old, e := os.ReadFile(target); e == nil {
			status = "conflict"
			if string(old) == string(data) {
				status = "skipped"
			}
		} else {
			if !os.IsNotExist(e) {
				return e
			}
			if err = os.WriteFile(target, data, 0600); err != nil {
				return err
			}
		}
		results = append(results, ImportResult{File: relative, Status: status, Note: "core context preferences and customizations; plugin expansion excluded"})
		return nil
	})
	if walkErr != nil {
		return results, walkErr
	}
	return results, nil
}

func translateSkin(data []byte) ([]byte, string) {
	var source map[string]any
	if yaml.Unmarshal(data, &source) != nil {
		return data, "unrecognized skin schema; review before use"
	}
	root, ok := source["k9s"].(map[string]any)
	if !ok {
		return data, ""
	}
	out := map[string]string{"name": "imported-k9s"}
	paths := map[string][]string{"fg": {"body", "fgColor"}, "bg": {"body", "bgColor"}, "accent": {"frame", "border", "focusColor"}, "selection": {"frame", "table", "cursor", "bgColor"}, "warn": {"frame", "status", "warnColor"}, "error": {"frame", "status", "errorColor"}, "subtle": {"frame", "border", "fgColor"}}
	for key, path := range paths {
		var value any = root
		for _, part := range path {
			m, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = m[part]
		}
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	converted, err := yaml.Marshal(out)
	if err != nil {
		return data, err.Error()
	}
	return converted, "translated shared colors; k9s component-specific styling is unsupported"
}
