//revive:disable:exported package-comments
package config

import (
	"errors"
	"os"
	"path/filepath"
)

type ImportResult struct {
	File   string
	Status string
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
		results = append(results, ImportResult{File: file.src, Status: "imported"})
	}
	return results, nil
}
