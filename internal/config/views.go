//revive:disable:exported package-comments
package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// ViewSpec is a per-resource custom column list.
type ViewSpec struct {
	Columns []string `yaml:"columns"`
}

type viewsFile struct {
	Views map[string]ViewSpec `yaml:"views"`
}

// LoadViews merges views.yaml files (later paths win). Keys are resource
// command strings ("v1/pods", "apps/v1/deployments"). Missing files skipped.
func LoadViews(paths ...string) map[string]ViewSpec {
	merged := map[string]ViewSpec{}
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- views file path is user configuration.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		var file viewsFile
		if yaml.Unmarshal(data, &file) != nil {
			continue
		}
		for key, spec := range file.Views {
			if len(spec.Columns) > 0 {
				merged[key] = spec
			}
		}
	}
	return merged
}
