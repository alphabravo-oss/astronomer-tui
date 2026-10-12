package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ContextDir encodes names so kubeconfig contexts cannot escape the config root.
func ContextDir(dir, cluster, context string) string {
	return filepath.Join(dir, "clusters", url.PathEscape(cluster), url.PathEscape(context))
}
func LoadScopedCustom(dir, cluster, context string) (Custom, error) {
	base, err := LoadCustom(dir)
	if err != nil {
		return base, err
	}
	if context == "" {
		return base, nil
	}
	overlay, err := LoadCustom(ContextDir(dir, cluster, context))
	if err != nil {
		return base, err
	}
	if base.Aliases == nil {
		base.Aliases = map[string]string{}
	}
	for k, v := range overlay.Aliases {
		base.Aliases[k] = v
	}
	if base.Hotkeys == nil {
		base.Hotkeys = map[string]Hotkey{}
	}
	for k, v := range overlay.Hotkeys {
		base.Hotkeys[k] = v
	}
	if base.Views == nil {
		base.Views = map[string]View{}
	}
	for k, v := range overlay.Views {
		base.Views[k] = v
	}
	for key := range base.Aliases {
		if _, err := base.Expand(key); err != nil {
			return base, err
		}
	}
	return base, nil
}

// Exact namespace wins, then deterministic namespace regexes, then the base view.
// An explicitly invoked alias has precedence over its underlying GVR.
func (c Custom) ViewFor(gvr, alias, namespace string) (View, bool) {
	bases := []string{gvr}
	if alias != "" {
		bases = []string{alias, gvr}
	}
	for _, base := range bases {
		if namespace != "" {
			if v, ok := c.Views[base+"@"+namespace]; ok {
				return v, true
			}
			var patterns []string
			for key := range c.Views {
				b, pattern, ok := strings.Cut(key, "@")
				if ok && b == base {
					patterns = append(patterns, pattern)
				}
			}
			sort.Strings(patterns)
			for _, pattern := range patterns {
				re, err := regexp.Compile(pattern)
				if err == nil && re.MatchString(namespace) {
					return c.Views[base+"@"+pattern], true
				}
			}
		}
		if v, ok := c.Views[base]; ok {
			return v, true
		}
	}
	return View{}, false
}

// ApplyContext overlays native preferences or the core fields of a k9s context
// file. It copies the map so a context override cannot mutate the global config.
func ApplyContext(dir, cluster, context string, base Config) (Config, error) {
	data, err := os.ReadFile(filepath.Join(ContextDir(dir, cluster, context), "config.yaml"))
	if os.IsNotExist(err) {
		return base, nil
	}
	if err != nil {
		return base, err
	}
	var source struct {
		Policy    string `yaml:"policy"`
		Theme     string `yaml:"theme"`
		Namespace string `yaml:"namespace"`
		View      string `yaml:"view"`
		K9s       *struct {
			ReadOnly  *bool  `yaml:"readOnly"`
			Skin      string `yaml:"skin"`
			Namespace struct {
				Active string `yaml:"active"`
			} `yaml:"namespace"`
			View struct {
				Active string `yaml:"active"`
			} `yaml:"view"`
		} `yaml:"k9s"`
	}
	if err = yaml.Unmarshal(data, &source); err != nil {
		return base, err
	}
	current := base.Contexts[context]
	if source.Policy != "" {
		current.Policy = source.Policy
	}
	if source.Theme != "" {
		current.Theme = source.Theme
	}
	if source.Namespace != "" {
		current.Namespace = source.Namespace
	}
	if source.View != "" {
		current.View = source.View
	}
	if source.K9s != nil {
		k := source.K9s
		if k.ReadOnly != nil && *k.ReadOnly {
			current.Policy = "read-only"
		}
		if k.Skin != "" {
			current.Theme = k.Skin
		}
		if k.Namespace.Active != "" {
			current.Namespace = k.Namespace.Active
		}
		if k.View.Active != "" {
			current.View = k.View.Active
		}
	}
	switch current.Policy {
	case "", "full", "read-only", "confirm-destructive":
	default:
		return base, fmt.Errorf("context policy %q is invalid", current.Policy)
	}
	clone := make(map[string]ContextConfig, len(base.Contexts)+1)
	for k, v := range base.Contexts {
		clone[k] = v
	}
	clone[context] = current
	base.Contexts = clone
	return base, nil
}
