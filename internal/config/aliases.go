//revive:disable:exported package-comments
package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// aliasesFile mirrors the k9s aliases.yaml schema.
type aliasesFile struct {
	Aliases map[string]string `yaml:"aliases"`
}

// LoadAliases loads and merges alias->target maps from the given paths (later
// paths win). Missing files are skipped. The target is a resource command
// string such as "v1/pods" or "apps/v1/deployments".
func LoadAliases(paths ...string) map[string]string {
	merged := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- alias file path is user configuration.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		var file aliasesFile
		if yaml.Unmarshal(data, &file) != nil {
			continue
		}
		for alias, target := range file.Aliases {
			merged[alias] = target
		}
	}
	return merged
}
