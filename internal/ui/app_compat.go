package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
)

func copyStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func copyHotKeys(input map[string]config.HotKey) map[string]config.HotKey {
	out := make(map[string]config.HotKey, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func (a *App) executeCommand(ctx context.Context, input string) bool {
	command, ok := parseCommand(input)
	if !ok {
		a.showMessage("Unknown command", ":"+strings.TrimPrefix(strings.TrimSpace(input), ":"))
		return false
	}
	switch command.Kind {
	case "resource":
		gvr, resolved := a.resolveResource(command.Value)
		if !resolved {
			a.showMessage("Unknown command", ":"+strings.TrimPrefix(strings.TrimSpace(input), ":"))
			return false
		}
		if a.switchResource(ctx, gvr) {
			a.app.SetRoot(a.table, true)
			return true
		}
	case "namespace":
		a.switchNamespace(command.Value)
		a.app.SetRoot(a.table, true)
		return true
	case "port-forward-manager":
		a.showPortForwardManager()
		return true
	case "auth-summary":
		a.showAuthSummary()
		return true
	case "health":
		a.showHealth()
		return true
	case "benchmark-manager":
		a.showBenchmarkManager()
		return true
	}
	return false
}

func (a *App) executeHotKey(event *tcell.EventKey, override bool) bool {
	shortcut := shortcutForEvent(event)
	if shortcut == "" {
		return false
	}
	hotkey, ok := a.options.HotKeys[shortcut]
	if !ok || hotkey.Override != override {
		return false
	}
	a.executeCommand(a.context(), hotkey.Command)
	return true
}

func (a *App) executePluginShortcut(event *tcell.EventKey, override bool) bool {
	shortcut := shortcutForEvent(event)
	if shortcut == "" {
		return false
	}
	names := make([]string, 0, len(a.pluginFile.Plugins))
	for name := range a.pluginFile.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	var matches []string
	for _, name := range names {
		plugin := a.pluginFile.Plugins[name]
		configured, err := config.CanonicalShortcut(plugin.ShortCut)
		if err == nil && configured == shortcut && plugin.Override == override && a.pluginScopeMatches(plugin) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return false
	}
	if len(matches) > 1 {
		a.showMessage("Plugin shortcut ambiguous", tview.Escape(fmt.Sprintf("%s matches %s", shortcut, strings.Join(matches, ", "))))
		return true
	}
	name := matches[0]
	return a.executePluginEntry(palette.Entry{ID: "plugin:" + name, Title: name, Category: palette.Plugins, Payload: a.pluginFile.Plugins[name]})
}

func (a *App) pluginScopeMatches(plugin plugins.Plugin) bool {
	a.mu.RLock()
	gvr := a.currentGVR
	a.mu.RUnlock()
	kind := ""
	if obj := a.selectedObject(); obj != nil {
		kind = strings.ToLower(obj.GetKind())
	}
	for _, rawScope := range plugin.Scopes {
		scope := strings.ToLower(strings.TrimSpace(rawScope))
		if scope == "all" || scope == "*" || (kind != "" && scope == kind) {
			return true
		}
		if scopedGVR, ok := a.resolveResource(scope); ok && scopedGVR == gvr {
			return true
		}
	}
	return false
}

func shortcutForEvent(event *tcell.EventKey) string {
	if event == nil {
		return ""
	}
	if event.Key() != tcell.KeyRune {
		name := strings.ReplaceAll(event.Name(), "+", "-")
		if canonical, err := config.CanonicalShortcut(name); err == nil {
			return canonical
		}
	}
	r := event.Rune()
	if r == 0 {
		return ""
	}
	if event.Modifiers()&tcell.ModAlt != 0 {
		return "Alt-" + strings.ToUpper(string(r))
	}
	shiftDigits := map[rune]string{')': "0", '!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7", '*': "8", '(': "9"}
	if digit, ok := shiftDigits[r]; ok {
		return "Shift-" + digit
	}
	if unicode.IsUpper(r) || event.Modifiers()&tcell.ModShift != 0 {
		return "Shift-" + strings.ToUpper(string(r))
	}
	canonical, _ := config.CanonicalShortcut(string(r))
	return canonical
}

func (a *App) helpText() string {
	lines := []string{HelpText(a.actionCatalog())}
	shortcuts := make([]string, 0, len(a.options.HotKeys))
	for shortcut := range a.options.HotKeys {
		shortcuts = append(shortcuts, shortcut)
	}
	sort.Strings(shortcuts)
	for _, shortcut := range shortcuts {
		hotkey := a.options.HotKeys[shortcut]
		description := hotkey.Description
		if description == "" {
			description = hotkey.Command
		}
		lines = append(lines, fmt.Sprintf("%s  hotkey - %s", tview.Escape(shortcut), tview.Escape(description)))
	}
	pluginNames := make([]string, 0, len(a.pluginFile.Plugins))
	for name := range a.pluginFile.Plugins {
		pluginNames = append(pluginNames, name)
	}
	sort.Strings(pluginNames)
	for _, name := range pluginNames {
		plugin := a.pluginFile.Plugins[name]
		if _, err := config.CanonicalShortcut(plugin.ShortCut); err == nil && a.pluginScopeMatches(plugin) {
			lines = append(lines, fmt.Sprintf("%s  plugin - %s", tview.Escape(plugin.ShortCut), tview.Escape(name)))
		}
	}
	return strings.Join(lines, "\n")
}
