//revive:disable:exported package-comments
package theme

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Skin struct {
	Name      string `yaml:"name"`
	FG        string `yaml:"fg"`
	BG        string `yaml:"bg"`
	Accent    string `yaml:"accent"`
	Warn      string `yaml:"warn"`
	Error     string `yaml:"error"`
	Subtle    string `yaml:"subtle"`
	Selection string `yaml:"selection"`
}

func Defaults() map[string]Skin {
	return map[string]Skin{
		"dark":  {Name: "dark", FG: "#dbe4f0", BG: "#000000", Accent: "#75d5f5", Warn: "#e9b86a", Error: "#ff7b86", Subtle: "#8592a3", Selection: "#263c59"},
		"light": {Name: "light", FG: "black", BG: "white", Accent: "blue", Warn: "orange", Error: "red", Subtle: "gray", Selection: "aqua"},
		"dim":   {Name: "dim", FG: "gray", BG: "black", Accent: "green", Warn: "yellow", Error: "maroon", Subtle: "darkgray", Selection: "teal"},
	}
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "astrotui", "skin.yaml")
	}
	return filepath.Join(home, ".config", "astrotui", "skin.yaml")
}

func Load(path string, fallback string) (Skin, error) {
	if fallback == "" {
		fallback = "dark"
	}
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- skin path is intentionally user-selectable.
		if len(bytes.TrimSpace(data)) == 0 {
			return Skin{}, errors.New("skin file is empty")
		}
		var skin Skin
		if err := yaml.Unmarshal(data, &skin); err != nil {
			return Skin{}, err
		}
		if skin.Name == "" {
			skin.Name = "custom"
		}
		return skin, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Skin{}, err
	}
	if skin, ok := Defaults()[fallback]; ok {
		return skin, nil
	}
	return Defaults()["dark"], nil
}
