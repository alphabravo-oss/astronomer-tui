//revive:disable:exported package-comments
package config

import (
	"errors"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Hotkey binds a key to a command.
type Hotkey struct {
	Key         string
	Command     string
	Description string
}

type hotkeyEntry struct {
	ShortCut    string `yaml:"shortCut"`
	Command     string `yaml:"command"`
	Description string `yaml:"description"`
}

type hotkeysFile struct {
	HotKeys map[string]hotkeyEntry `yaml:"hotKeys"`
}

// LoadHotkeys reads and merges hotkeys.yaml files (k9s schema). The binding key
// is the entry's shortCut, falling back to the map key. Later paths win per key.
func LoadHotkeys(paths ...string) []Hotkey {
	merged := map[string]Hotkey{}
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- hotkeys file path is user configuration.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		var file hotkeysFile
		if yaml.Unmarshal(data, &file) != nil {
			continue
		}
		for id, entry := range file.HotKeys {
			if entry.Command == "" {
				continue
			}
			key := entry.ShortCut
			if key == "" {
				key = id
			}
			merged[key] = Hotkey{Key: key, Command: entry.Command, Description: entry.Description}
		}
	}
	out := make([]Hotkey, 0, len(merged))
	for _, hk := range merged {
		out = append(out, hk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
