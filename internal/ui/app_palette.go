//revive:disable:exported package-comments
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sort"
	"strings"
	"time"
)

func (a *App) showPalette() {
	index := palette.NewIndex(a.paletteEntries())
	list := tview.NewList().ShowSecondaryText(true)
	input := tview.NewInputField().
		SetLabel("> ").
		SetFieldWidth(0)
	var entriesByRow map[int]palette.Entry
	refresh := func(query string) {
		matches := index.Search(query, 50)
		if aiEntry, ok := a.aiPaletteEntry(query); ok {
			matches = []palette.Match{{Entry: aiEntry}}
		}
		entriesByRow = populatePaletteList(list, matches, a.executePaletteEntry)
		for row := 0; row < list.GetItemCount(); row++ {
			if _, ok := entriesByRow[row]; ok {
				list.SetCurrentItem(row)
				break
			}
		}
	}
	refresh("")
	input.SetChangedFunc(refresh)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if entry, ok := entriesByRow[list.GetCurrentItem()]; ok {
				a.executePaletteEntry(entry)
				return
			}
		}
		a.app.SetRoot(a.table, true)
	})
	list.SetSelectedFunc(func(row int, _ string, _ string, _ rune) {
		if entry, ok := entriesByRow[row]; ok {
			a.executePaletteEntry(entry)
		}
	})
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(input, 1, 0, true).
		AddItem(list, 0, 1, false)
	flex.SetTitle(" Palette ").SetBorder(true)
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(flex, true).SetFocus(input)
}

func populatePaletteList(list *tview.List, matches []palette.Match, execute func(palette.Entry) bool) map[int]palette.Entry {
	list.Clear()
	entriesByRow := map[int]palette.Entry{}
	for _, category := range []palette.Category{palette.Resources, palette.Actions, palette.Contexts, palette.Namespaces, palette.Plugins, palette.AI} {
		var grouped []palette.Entry
		for _, match := range matches {
			if match.Entry.Category == category {
				grouped = append(grouped, match.Entry)
			}
		}
		if len(grouped) == 0 {
			continue
		}
		list.AddItem(fmt.Sprintf("[aqua::b]%s", category), "", 0, nil)
		for _, entry := range grouped {
			row := list.GetItemCount()
			entry := entry
			list.AddItem(entry.Title, entry.Description, 0, func() {
				execute(entry)
			})
			entriesByRow[row] = entry
		}
	}
	if list.GetItemCount() == 0 {
		list.AddItem("No matches", "", 0, nil)
	}
	return entriesByRow
}

func (a *App) executePaletteEntry(entry palette.Entry) bool {
	switch entry.Category {
	case palette.Resources:
		if strings.HasPrefix(entry.ID, "resource:") {
			gvr, ok := resourceCommand(strings.TrimPrefix(entry.ID, "resource:"))
			if !ok {
				return false
			}
			return a.switchResource(context.Background(), gvr)
		}
		if obj, ok := entry.Payload.(*unstructured.Unstructured); ok {
			a.showDetailFor(obj)
			return true
		}
	case palette.Namespaces:
		if strings.HasPrefix(entry.ID, "namespace:") {
			a.switchNamespace(strings.TrimPrefix(entry.ID, "namespace:"))
			return true
		}
	case palette.Contexts:
		if strings.HasPrefix(entry.ID, "context:") {
			return a.switchContext(context.Background(), strings.TrimPrefix(entry.ID, "context:"))
		}
	case palette.Actions:
		return a.executeActionEntry(entry)
	case palette.Plugins:
		return a.executePluginEntry(entry)
	case palette.AI:
		return a.executeAIEntry(entry)
	}
	return false
}

func (a *App) aiPaletteEntry(query string) (palette.Entry, bool) {
	query = strings.TrimSpace(query)
	if query == "" || (query[0] != '>' && query[0] != '?') {
		return palette.Entry{}, false
	}
	intent := strings.TrimSpace(query[1:])
	if intent == "" {
		return palette.Entry{}, false
	}
	description := "AI is not configured"
	if a.options.AIProvider != nil {
		description = "Ask AI to map intent to an action or clarifying question"
	}
	return palette.Entry{ID: "ai:nl-action", Title: intent, Description: description, Category: palette.AI, Payload: intent}, true
}

func (a *App) executeAIEntry(entry palette.Entry) bool {
	intent, ok := entry.Payload.(string)
	if !ok || strings.TrimSpace(intent) == "" {
		return false
	}
	if a.options.AIProvider == nil {
		a.showMessage("AI disabled", "Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := ai.NaturalLanguageAction(ctx, a.options.AIProvider, HelpText(a.actionCatalog()), intent)
	if err != nil {
		a.showMessage("AI failed", err.Error())
		return true
	}
	if actionName, ok := parseAIActionCall(resp.Text); ok {
		if a.actionAvailable(actionName) {
			return a.executeActionEntry(palette.Entry{ID: "action:" + actionName, Title: actionName, Category: palette.Actions})
		}
		a.showMessage("AI action unavailable", fmt.Sprintf("%q is not available for the current selection", actionName))
		return true
	}
	a.showMessage("AI", resp.Text)
	return true
}

func (a *App) executeActionEntry(entry palette.Entry) bool {
	actionName := strings.TrimPrefix(entry.ID, "action:")
	switch actionName {
	case "delete":
		a.showDelete()
	case "edit":
		a.showEdit()
	case "scale":
		a.showScale()
	case "restart":
		a.showRestart()
	case "logs":
		a.showLogsPlan()
	case "exec":
		a.showExecPlan()
	case "port-forward":
		a.showPortForwardPlan()
	case "describe":
		a.showDetail()
	default:
		return false
	}
	return true
}

func (a *App) executePluginEntry(entry palette.Entry) bool {
	plugin, ok := entry.Payload.(plugins.Plugin)
	if !ok {
		return false
	}
	plan := plugins.Plan(plugin, a.pluginVars())
	if plan.Confirm {
		a.showPluginConfirm(entry.Title, plan)
		return true
	}
	a.runPluginPlan(entry.Title, plan)
	return true
}

func (a *App) showPluginConfirm(name string, plan plugins.CommandPlan) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Run plugin %s?\n%s %s", name, plan.Command, strings.Join(plan.Args, " "))).
		AddButtons([]string{"Run", "Cancel"}).
		SetDoneFunc(func(index int, _ string) {
			if index == 0 {
				a.runPluginPlan(name, plan)
				return
			}
			a.app.SetRoot(a.table, true)
		})
	a.app.SetRoot(modal, false)
}

func (a *App) runPluginPlan(name string, plan plugins.CommandPlan) {
	executor := a.options.PluginExecutor
	if executor == nil {
		executor = func(plan plugins.CommandPlan) error {
			cmd := plugins.Command(plan)
			if plan.Background {
				return cmd.Start()
			}
			return cmd.Run()
		}
	}
	var err error
	run := func() {
		err = executor(plan)
	}
	if plan.Background || !a.app.Suspend(run) {
		run()
	}
	if err != nil {
		a.showMessage("Plugin failed", err.Error())
		return
	}
	a.showMessage("Plugin", fmt.Sprintf("ran %s", name))
}

func (a *App) pluginVars() map[string]string {
	vars := map[string]string{}
	if a.options.Client != nil {
		vars["CONTEXT"] = a.options.Client.Context
		vars["NAMESPACE"] = a.options.Client.Namespace
	}
	if obj := a.selectedObject(); obj != nil {
		vars["NAME"] = obj.GetName()
		if obj.GetNamespace() != "" {
			vars["NAMESPACE"] = obj.GetNamespace()
		}
	}
	return vars
}

func (a *App) paletteEntries() []palette.Entry {
	a.mu.RLock()
	objects := append([]*unstructured.Unstructured(nil), a.objects...)
	currentGVR := a.currentGVR
	a.mu.RUnlock()
	entries := palette.ResourceEntries(fallbackKind(currentGVR.Resource), objects)
	for _, alias := range ResourceAliasEntries() {
		entries = append(entries, palette.Entry{ID: "resource:" + alias, Title: alias, Category: palette.Resources})
	}
	var actionNames []string
	for _, action := range a.actionCatalog() {
		actionNames = append(actionNames, action.Name)
	}
	entries = append(entries, palette.ActionEntries(actionNames)...)
	var contexts []string
	for name := range a.options.Client.RawConfig.Contexts {
		contexts = append(contexts, name)
	}
	sort.Strings(contexts)
	entries = append(entries, palette.ContextEntries(contexts)...)
	entries = append(entries, palette.NamespaceEntries(a.namespaceNames())...)
	entries = append(entries, palette.PluginEntries(a.pluginFile)...)
	return entries
}

func (a *App) actionCatalog() []actions.Action {
	kind := ""
	if obj := a.selectedObject(); obj != nil {
		kind = obj.GetKind()
	}
	if kind == "" {
		a.mu.RLock()
		kind = fallbackKind(a.currentGVR.Resource)
		a.mu.RUnlock()
	}
	return actions.CatalogFor(kind)
}

func (a *App) actionAvailable(name string) bool {
	for _, action := range a.actionCatalog() {
		if action.Name == name {
			return true
		}
	}
	return false
}

func parseAIActionCall(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	var decoded struct {
		Action string `json:"action"`
		Verb   string `json:"verb"`
	}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return "", false
	}
	action := strings.TrimSpace(decoded.Action)
	if action == "" {
		action = strings.TrimSpace(decoded.Verb)
	}
	return action, action != ""
}
