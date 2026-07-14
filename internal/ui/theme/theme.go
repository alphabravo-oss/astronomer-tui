//revive:disable:exported package-comments
package theme

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
		"dark":  {Name: "dark", FG: "white", BG: "black", Accent: "aqua", Warn: "yellow", Error: "red", Subtle: "gray", Selection: "blue"},
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
	if data, err := readSkin(path); err == nil {
		var envelope k9sSkinEnvelope
		if err := yaml.Unmarshal(data, &envelope); err != nil {
			return Skin{}, err
		}
		if envelope.K9s.present() {
			return envelope.K9s.toSkin(fallback), nil
		}
		var skin Skin
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&skin); err != nil {
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

type k9sSkinEnvelope struct {
	K9s k9sSkin `yaml:"k9s"`
}

type k9sSkin struct {
	Body struct {
		FGColor string `yaml:"fgColor"`
		BGColor string `yaml:"bgColor"`
	} `yaml:"body"`
	Frame struct {
		Border struct {
			FGColor    string `yaml:"fgColor"`
			FocusColor string `yaml:"focusColor"`
		} `yaml:"border"`
		Status struct {
			ErrorColor   string `yaml:"errorColor"`
			PendingColor string `yaml:"pendingColor"`
		} `yaml:"status"`
	} `yaml:"frame"`
	Views struct {
		Table struct {
			FGColor       string `yaml:"fgColor"`
			BGColor       string `yaml:"bgColor"`
			CursorBGColor string `yaml:"cursorBgColor"`
			Header        struct {
				FGColor string `yaml:"fgColor"`
			} `yaml:"header"`
		} `yaml:"table"`
	} `yaml:"views"`
}

func (k k9sSkin) present() bool {
	return k.Body.FGColor != "" || k.Body.BGColor != "" || k.Views.Table.FGColor != "" || k.Frame.Border.FGColor != ""
}

func (k k9sSkin) toSkin(fallback string) Skin {
	base, ok := Defaults()[fallback]
	if !ok {
		base = Defaults()["dark"]
	}
	base.Name = "k9s"
	base.FG = firstColor(k.Views.Table.FGColor, k.Body.FGColor, base.FG)
	base.BG = firstColor(k.Views.Table.BGColor, k.Body.BGColor, base.BG)
	base.Accent = firstColor(k.Views.Table.Header.FGColor, k.Frame.Border.FocusColor, base.Accent)
	base.Warn = firstColor(k.Frame.Status.PendingColor, base.Warn)
	base.Error = firstColor(k.Frame.Status.ErrorColor, base.Error)
	base.Subtle = firstColor(k.Frame.Border.FGColor, base.Subtle)
	base.Selection = firstColor(k.Views.Table.CursorBGColor, base.Selection)
	return base
}

func firstColor(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func readSkin(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- skin path is intentionally user-selectable.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	const maxSkinBytes = int64(1 << 20)
	data, err := io.ReadAll(io.LimitReader(file, maxSkinBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSkinBytes {
		return nil, fmt.Errorf("skin file exceeds %d bytes", maxSkinBytes)
	}
	return data, nil
}
