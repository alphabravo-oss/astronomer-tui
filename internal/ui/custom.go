package ui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
	uitheme "github.com/alphabravo-oss/astrotui/internal/ui/theme"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"github.com/gdamore/tcell/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *App) customDir() string {
	if a.options.ConfigDir != "" {
		return a.options.ConfigDir
	}
	return config.DefaultDir()
}
func (a *App) watchCustom(ctx context.Context) {
	if a.customCancel != nil {
		a.customCancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.customCancel = cancel
	dir := a.customDir()
	contextName, clusterName := "", ""
	if client := a.options.Client; client != nil {
		contextName = client.Context
		if c := client.RawConfig.Contexts[contextName]; c != nil {
			clusterName = c.Cluster
		}
	}
	baseConfig := a.baseConfig
	readOnly := a.options.ReadOnly
	paths := pluginPaths(a.options.PluginPaths)
	go func() {
		var previous [32]byte
		for {
			hash := sha256.New()
			_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err == nil && d.IsDir() && (d.Name() == "benchmarks" || d.Name() == "screendumps") {
					return filepath.SkipDir
				}
				if err == nil && !d.IsDir() {
					if b, e := os.ReadFile(path); e == nil {
						_, _ = hash.Write([]byte(path))
						_, _ = hash.Write(b)
					}
				}
				return nil
			})
			var current [32]byte
			copy(current[:], hash.Sum(nil))
			if current != previous {
				custom, err := config.LoadScopedCustom(dir, clusterName, contextName)
				loaded := baseConfig
				var provider ai.LLMProvider
				if _, e := os.Stat(filepath.Join(dir, "config.yaml")); e == nil {
					var loadErr error
					loaded, loadErr = config.Load(filepath.Join(dir, "config.yaml"))
					if loadErr != nil {
						err = loadErr
					}
				}
				if readOnly {
					loaded.ReadOnly = true
				}
				baseLoaded := loaded
				if err == nil {
					var overlayErr error
					loaded, overlayErr = config.ApplyContext(dir, clusterName, contextName, loaded)
					if overlayErr != nil {
						err = overlayErr
					}
				}
				if err == nil {
					var providerErr error
					provider, providerErr = ai.NewProvider(loaded.AI)
					if providerErr != nil {
						err = providerErr
					}
				}
				cfg := &loaded

				file, pluginErr := plugins.LoadMerged(paths...)
				if err == nil {
					err = pluginErr
				}
				a.queueUpdate(ctx, func() {
					if err != nil {
						a.customError = err
						a.updateChrome()
						return
					}
					a.customError = nil
					a.custom = custom
					if cfg != nil {
						a.baseConfig = baseLoaded
						a.options.Config = *cfg
						a.options.AIProvider = provider
						if _, e := os.Stat(filepath.Join(dir, "skin.yaml")); os.IsNotExist(e) {
							if skin, ok := uitheme.Defaults()[cfg.Theme]; ok {
								a.options.Skin = skin
							}
						}
						a.applySkinUpdate(a.options.Skin)
					}
					a.pluginFile = file
					filter := a.filter
					predicate := a.relatedPredicate
					a.switchResource(a.backgroundContext(), a.currentGVR)
					a.filter = filter
					a.relatedPredicate = predicate
					a.reloadCurrent()
				})
				if err == nil {
					previous = current
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
func (a *App) configuredRenderer(base views.Renderer) views.Renderer {
	v, ok := a.currentCustomView(base.GVR())
	if !ok {
		return base
	}
	r := views.CustomRenderer{Renderer: base}
	used := map[string]bool{}
	for _, spec := range v.Columns {
		raw, attrs, _ := strings.Cut(spec, "|")
		name, path, _ := strings.Cut(raw, ":")
		name = strings.ToUpper(name)
		used[name] = true
		if strings.Contains(attrs, "H") {
			continue
		}
		col := views.Column{Name: name}
		for _, c := range base.Header() {
			if c.Name == name {
				col = c
				break
			}
		}
		if strings.Contains(attrs, "W") {
			col.Priority = 1
		}
		if strings.Contains(attrs, "S") {
			col.Priority = 0
		}
		if strings.Contains(attrs, "L") {
			col.AlignRight = false
		}
		if strings.Contains(attrs, "N") {
			col.Type = "number"
		}
		if strings.Contains(attrs, "T") {
			col.Type = "date"
		}
		if strings.Contains(attrs, "R") {
			col.AlignRight = true
		}
		r.Columns = append(r.Columns, col)
		r.Paths = append(r.Paths, path)
	}
	for _, col := range base.Header() {
		if !used[col.Name] {
			col.Priority = 1
			r.Columns = append(r.Columns, col)
			r.Paths = append(r.Paths, "")
		}
	}
	if len(r.Columns) == 0 {
		return base
	}
	return r
}
func (a *App) executeConfiguredCommand(command string) {
	expanded, err := a.custom.Expand(command)
	if err != nil {
		a.showMessage("Command", err.Error())
		return
	}
	if a.specialCommand(expanded) {
		return
	}
	parsed, ok := parseCommand(expanded)
	if !ok {
		a.showMessage("Command", "Unsupported command: "+expanded)
		return
	}
	switch parsed.Kind {
	case "resource":
		parts := strings.Fields(parsed.Value)
		g, err := a.resolveResource(parts[0])
		if err != nil {
			a.showMessage("Command", err.Error())
			return
		}
		filter, namespace, nextContext := "", "", ""
		for i := 1; i < len(parts); i++ {
			part := parts[i]
			switch {
			case strings.HasPrefix(part, "@"):
				nextContext = strings.TrimPrefix(part, "@")
			case strings.HasPrefix(part, "/"):
				filter = strings.TrimPrefix(strings.Join(parts[i:], " "), "/")
				i = len(parts)
			case strings.ContainsAny(part, "=!"):
				filter = "-l " + part
			default:
				if namespace != "" {
					a.showMessage("Command", "Only one namespace may be specified")
					return
				}
				namespace = part
			}
		}
		if _, err := compileResourceFilter(filter); err != nil {
			a.showMessage("Command", err.Error())
			return
		}
		if nextContext != "" && nextContext != a.options.Client.Context {
			var remaining []string
			for _, part := range parts {
				if !strings.HasPrefix(part, "@") {
					remaining = append(remaining, part)
				}
			}
			a.switchContextAfter(a.backgroundContext(), nextContext, func() { a.executeConfiguredCommand(strings.Join(remaining, " ")) })
			return
		}
		if namespace != "" {
			a.options.Client.Namespace = namespace
			if namespace == "all" || namespace == "*" {
				a.options.Client.Namespace = ""
			}
		}
		a.viewAlias = ""
		original := strings.Fields(strings.TrimPrefix(command, ":"))
		if len(original) > 0 {
			if _, ok := a.custom.Aliases[original[0]]; ok {
				a.viewAlias = original[0]
				a.viewAliasGVR = g
			}
		}
		a.switchResource(a.backgroundContext(), g)
		a.filter = filter
		a.reloadCurrent()
		a.showMain()
	case "namespace":
		if parsed.Value == "" {
			a.showNamespacePicker()
		} else {
			a.switchNamespace(parsed.Value)
			a.showMain()
		}
	case "context":
		if parsed.Value == "" {
			a.showContextPicker(a.backgroundContext())
		} else {
			a.switchContext(a.backgroundContext(), parsed.Value)
		}
	case "refresh":
		a.refreshDiscovery()
		a.showMain()
	default:
		a.showMessage("Command", fmt.Sprintf("%s is unavailable as a custom shortcut", parsed.Kind))
	}
}
func keyMatches(e *tcell.EventKey, shortcut string) bool {
	normalize := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), " ", ""))
	}
	if normalize(e.Name()) == normalize(shortcut) {
		return true
	}
	if e.Key() == tcell.KeyRune && string(e.Rune()) == shortcut {
		return true
	}
	if strings.HasPrefix(strings.ToLower(shortcut), "shift+") || strings.HasPrefix(strings.ToLower(shortcut), "shift-") {
		shifted := strings.ToUpper(shortcut[6:])
		if len(shifted) == 1 {
			if i := strings.IndexByte("1234567890", shifted[0]); i >= 0 {
				shifted = string("!@#$%^&*()"[i])
			}
		}
		return e.Key() == tcell.KeyRune && string(e.Rune()) == shifted
	}
	return false
}
func (a *App) customKey(e *tcell.EventKey) bool {
	names := make([]string, 0, len(a.custom.Hotkeys))
	for name := range a.custom.Hotkeys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := a.custom.Hotkeys[name]
		if keyMatches(e, h.ShortCut) {
			a.executeConfiguredCommand(h.Command)
			return true
		}
	}
	names = nil
	for name := range a.pluginFile.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := a.pluginFile.Plugins[name]
		if a.pluginInScope(p) && keyMatches(e, p.ShortCut) {
			a.executePluginEntry(palette.Entry{Title: name, Payload: p})
			return true
		}
	}
	return false
}
func (a *App) pluginInScope(p plugins.Plugin) bool {
	if len(p.Scopes) == 0 {
		return true
	}
	for _, scope := range p.Scopes {
		if scope == "all" || scope == "*" {
			return true
		}
		g, err := a.resolveResource(scope)
		if err == nil && g == a.currentGVR {
			return true
		}
	}
	return false
}

func (a *App) currentCustomView(g schema.GroupVersionResource) (config.View, bool) {
	alias := ""
	if a.viewAliasGVR == g {
		alias = a.viewAlias
	}
	ns := ""
	if a.options.Client != nil {
		ns = a.options.Client.Namespace
	}
	return a.custom.ViewFor(cluster.Qualified(g), alias, ns)
}

func (a *App) contextNames() (string, string) {
	if client := a.options.Client; client != nil {
		if c := client.RawConfig.Contexts[client.Context]; c != nil {
			return client.Context, c.Cluster
		}
		return client.Context, ""
	}
	return "", ""
}
