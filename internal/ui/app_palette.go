//revive:disable:exported package-comments
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"os"
	"sort"
	"strings"
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
		a.showMain()
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
		if event.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		delta := 0
		switch event.Key() {
		case tcell.KeyDown, tcell.KeyCtrlN, tcell.KeyTab:
			delta = 1
		case tcell.KeyUp, tcell.KeyCtrlP, tcell.KeyBacktab:
			delta = -1
		}
		if delta != 0 {
			row := list.GetCurrentItem()
			for i := 0; i < list.GetItemCount(); i++ {
				row = (row + delta + list.GetItemCount()) % list.GetItemCount()
				if _, ok := entriesByRow[row]; ok {
					list.SetCurrentItem(row)
					break
				}
			}
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
			list.AddItem(tview.Escape(entry.Title), tview.Escape(entry.Description), 0, func() {
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
	if command, ok := strings.CutPrefix(entry.ID, "view:"); ok {
		return a.specialCommand(command)
	}
	switch entry.Category {
	case palette.Resources:
		if strings.HasPrefix(entry.ID, "resource:") {
			command := strings.TrimPrefix(strings.TrimPrefix(entry.ID, "resource:"), ":")
			if _, exists := a.custom.Aliases[command]; exists {
				a.executeConfiguredCommand(command)
				return true
			}
			gvr, err := a.resolveResource(command)
			if err != nil {
				a.showMessage("Resource", err.Error())
				return false
			}
			a.viewAlias = ""
			if !a.switchResource(a.backgroundContext(), gvr) {
				return false
			}
			a.showMain()
			return true
		}
		if obj, ok := entry.Payload.(*unstructured.Unstructured); ok {
			a.showDetailFor(obj)
			return true
		}
	case palette.Namespaces:
		if strings.HasPrefix(entry.ID, "namespace:") {
			a.switchNamespace(strings.TrimPrefix(entry.ID, "namespace:"))
			a.showMain()
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
	provider := a.options.AIProvider
	req := ai.Request{System: "Suggest a Kubernetes TUI action or ask a clarifying question. Never claim an action has been executed.", Prompt: HelpText(a.actionCatalog()) + "\nUser intent: " + intent}
	a.previewAI(req, func(prepared ai.Request) {
		a.operation("Ask AI", func(ctx context.Context) (func(), error) {
			response, err := provider.Complete(ctx, prepared)
			if err != nil {
				return nil, err
			}
			return func() { a.showMessage("AI suggestion — review and run manually", response.Text) }, nil
		})
	}, a.showMain)
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
		a.showDescribe()
	case "cordon", "uncordon", "drain", "trigger-job", "suspend", "resume":
		a.showAdministrativeAction(actionName)
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
	if a.options.Client != nil && safety.PolicyFor(a.options.Config, a.options.Client.Context) == "read-only" {
		a.showMessage("Plugin unavailable", "External commands are disabled in read-only contexts.")
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
			a.showMain()
		})
	a.app.SetRoot(modal, false)
}

func (a *App) runPluginPlan(name string, plan plugins.CommandPlan) {
	executor := a.options.PluginExecutor
	if executor == nil {
		executor = func(plan plugins.CommandPlan) error {
			cmd := plugins.Command(plan)
			if plan.Background {
				if err := cmd.Start(); err != nil {
					return err
				}
				go func() { _ = cmd.Wait() }()
				return nil
			}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
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
	vars := map[string]string{
		"RESOURCE_GROUP": a.currentGVR.Group, "RESOURCE_VERSION": a.currentGVR.Version,
		"RESOURCE_NAME": a.currentGVR.Resource, "FILTER": a.filter,
	}
	if a.options.Client != nil {
		vars["CONTEXT"] = a.options.Client.Context
		vars["KUBECONFIG"] = a.options.Client.KubeconfigPath
		if c := a.options.Client.RawConfig.Contexts[a.options.Client.Context]; c != nil {
			vars["CLUSTER"], vars["USER"] = c.Cluster, c.AuthInfo
		}
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
	for _, command := range explorerCommands {
		entries = append(entries, palette.Entry{ID: "view:" + command, Title: ":" + command, Description: "Core explorer", Category: palette.Actions})
	}
	for _, alias := range ResourceAliasEntries() {
		entries = append(entries, palette.Entry{ID: "resource:" + alias, Title: alias, Category: palette.Resources})
	}
	if a.catalog != nil {
		for _, r := range a.catalog.Resources {
			name := cluster.Qualified(r.GVR)
			entries = append(entries, palette.Entry{ID: "resource:" + name, Title: ":" + name, Description: r.Kind + " " + strings.Join(r.ShortNames, ", "), Category: palette.Resources})
		}
	}
	for alias, target := range a.custom.Aliases {
		entries = append(entries, palette.Entry{ID: "resource:" + alias, Title: ":" + alias, Description: target, Category: palette.Resources})
	}
	var actionNames []string
	for _, action := range a.actionCatalog() {
		actionNames = append(actionNames, action.Name)
	}
	entries = append(entries, palette.ActionEntries(actionNames)...)
	var contexts []string
	if a.options.Client != nil {
		for name := range a.options.Client.RawConfig.Contexts {
			contexts = append(contexts, name)
		}
	}
	sort.Strings(contexts)
	entries = append(entries, palette.ContextEntries(contexts)...)
	entries = append(entries, palette.NamespaceEntries(append([]string{"all"}, a.namespaceNames()...))...)
	scoped := plugins.File{Plugins: map[string]plugins.Plugin{}}
	for name, p := range a.pluginFile.Plugins {
		if a.pluginInScope(p) {
			scoped.Plugins[name] = p
		}
	}
	entries = append(entries, palette.PluginEntries(scoped)...)
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
