//revive:disable:exported package-comments
package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/palette"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/alphabravo-oss/astrotui/internal/session"
	uitheme "github.com/alphabravo-oss/astrotui/internal/ui/theme"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

type Options struct {
	Client         *cluster.Client
	Config         config.Config
	SessionStore   *session.Store
	InitialState   session.State
	Skin           uitheme.Skin
	SkinUpdates    <-chan uitheme.Update
	PluginPaths    []string
	PluginExecutor func(plugins.CommandPlan) error
	ClientFactory  func(context.Context, cluster.ClientOptions) (*cluster.Client, error)
	Editor         actions.Editor
	ExecRunner     func(context.Context, actions.ExecTarget) error
	PortForwards   *actions.PortForwardManager
	PortForwardRun func(context.Context, actions.PortForwardSpec) error
	AIProvider     ai.LLMProvider
	LogStreamer    func(context.Context, logs.PodLogTarget, logs.RequestOptions, io.Writer) error
	Version        string
}

type App struct {
	options        Options
	app            *tview.Application
	table          *tview.Table
	cache          *cluster.ResourceCache
	currentGVR     schema.GroupVersionResource
	objects        []*unstructured.Unstructured
	visibleObjects []*unstructured.Unstructured
	filter         string
	sortCol        int
	sortDesc       bool
	mu             sync.RWMutex
	resourceCancel context.CancelFunc
	crashes        CrashReporter
	renderer       views.Renderer
	pluginFile     plugins.File
	skin           uitheme.Skin
}

func New(options Options) *App {
	pluginFile, _ := plugins.LoadMerged(pluginPaths(options.PluginPaths)...)
	app := &App{
		options:    options,
		app:        tview.NewApplication(),
		table:      tview.NewTable().SetSelectable(true, false),
		crashes:    CrashReporter{Dir: DefaultCrashDir()},
		pluginFile: pluginFile,
		skin:       resolveSkin(options),
	}
	app.applySkin()
	return app
}

func (a *App) Run(ctx context.Context) error {
	gvr := initialResourceGVR(a.options.InitialState)
	a.cache = cluster.NewResourceCache(a.options.Client.Dynamic, 30*time.Minute)
	a.cache.Start(ctx)
	a.switchResource(ctx, gvr)
	if a.options.InitialState.LastFilter != "" {
		a.mu.Lock()
		a.filter = a.options.InitialState.LastFilter
		a.mu.Unlock()
	}
	a.watchSkinUpdates(ctx)

	a.table.SetTitle(fmt.Sprintf(" astrotui [%s/%s] %s ", a.options.Client.Context, a.options.Client.Namespace, gvr.Resource)).SetBorder(true)
	a.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == '?' {
			a.safe(a.showHelp)
			return nil
		}
		if event.Key() == tcell.KeyCtrlP {
			a.safe(a.showPalette)
			return nil
		}
		if event.Key() == tcell.KeyCtrlE {
			a.safe(a.showFailureAI)
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			a.safe(a.showDetail)
			return nil
		}
		if event.Rune() == '/' {
			a.safe(a.showFilter)
			return nil
		}
		if event.Rune() == ':' {
			a.safe(func() { a.showCommand(ctx) })
			return nil
		}
		if event.Rune() == 'd' {
			a.safe(a.showDelete)
			return nil
		}
		if event.Rune() == 'e' {
			a.safe(a.showEdit)
			return nil
		}
		if event.Rune() == 'l' {
			a.safe(a.showLogsPlan)
			return nil
		}
		if event.Rune() == 'x' {
			a.safe(a.showExecPlan)
			return nil
		}
		if event.Rune() == 'f' {
			a.safe(a.showPortForwardPlan)
			return nil
		}
		if event.Rune() == 's' {
			a.safe(a.showScale)
			return nil
		}
		if event.Rune() == 'r' {
			a.safe(a.showRestart)
			return nil
		}
		if col, ok := sortColumnFromRune(event.Rune()); ok {
			a.setSort(col)
			return nil
		}
		return event
	})
	a.showSplash(ctx)
	err := a.app.Run()
	if a.options.SessionStore != nil {
		_ = a.options.SessionStore.Save(a.sessionState())
	}
	return err
}

func initialResourceGVR(state session.State) schema.GroupVersionResource {
	if gvr, ok := resourceCommand(state.LastView); ok {
		return gvr
	}
	return schema.GroupVersionResource{Version: "v1", Resource: "pods"}
}

func (a *App) sessionState() session.State {
	state := session.State{}
	if a.options.Client != nil {
		state.Context = a.options.Client.Context
		state.Namespace = a.options.Client.Namespace
	}
	a.mu.RLock()
	state.LastView = a.currentGVR.Resource
	state.LastFilter = a.filter
	a.mu.RUnlock()
	if state.LastView == "" {
		state.LastView = "pods"
	}
	return state
}

func (a *App) selectedObject() *unstructured.Unstructured {
	row, _ := a.table.GetSelection()
	return selectedObject(a.visibleObjectsSnapshot(), row)
}

func (a *App) visibleObjectsSnapshot() []*unstructured.Unstructured {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]*unstructured.Unstructured(nil), a.visibleObjects...)
}

func selectedObject(objects []*unstructured.Unstructured, tableRow int) *unstructured.Unstructured {
	index := tableRow - 1
	if index < 0 || index >= len(objects) {
		return nil
	}
	return objects[index]
}

func (a *App) drawHeader(renderer views.Renderer) {
	a.mu.RLock()
	sortCol, sortDesc := a.sortCol, a.sortDesc
	a.mu.RUnlock()
	for col, header := range renderer.Header() {
		name := header.Name
		if col == sortCol {
			if sortDesc {
				name += " ▼"
			} else {
				name += " ▲"
			}
		}
		a.table.SetCell(0, col, tview.NewTableCell(name).SetTextColor(skinColor(a.skin.Accent, tcell.ColorAqua)).SetSelectable(false))
	}
}

func (a *App) applySkin() {
	a.table.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	a.table.SetBorderColor(skinColor(a.skin.Subtle, tcell.ColorGray))
	a.table.SetTitleColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	a.table.SetSelectedStyle(tcell.StyleDefault.
		Foreground(skinColor(a.skin.FG, tcell.ColorWhite)).
		Background(skinColor(a.skin.Selection, tcell.ColorBlue)))
}

func (a *App) watchSkinUpdates(ctx context.Context) {
	if a.options.SkinUpdates == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-a.options.SkinUpdates:
				if !ok {
					return
				}
				if update.Err != nil {
					continue
				}
				a.app.QueueUpdateDraw(func() {
					a.applySkinUpdate(update.Skin)
				})
			}
		}
	}()
}

func (a *App) applySkinUpdate(skin uitheme.Skin) {
	a.skin = skin
	a.applySkin()
	if a.renderer != nil {
		a.drawHeader(a.renderer)
	}
	a.updateTitle()
}

func (a *App) refresh(cache *cluster.ResourceCache, client *cluster.Client, gvr schema.GroupVersionResource, renderer views.Renderer) {
	a.mu.RLock()
	current := a.currentGVR
	a.mu.RUnlock()
	if current != gvr {
		return
	}
	namespace := namespaceFor(client, gvr)
	items, err := cache.List(gvr, namespace, labels.Everything())
	if err != nil {
		return
	}
	a.mu.RLock()
	filter, sortCol, sortDesc := a.filter, a.sortCol, a.sortDesc
	a.mu.RUnlock()
	rows, visible := buildRows(renderer, items, filter, sortCol, sortDesc)
	a.mu.Lock()
	a.objects = items
	a.visibleObjects = visible
	a.mu.Unlock()
	a.app.QueueUpdateDraw(func() {
		a.table.Clear()
		a.drawHeader(renderer)
		for row, cells := range rows {
			for col, cell := range cells {
				a.table.SetCell(row+1, col, tview.NewTableCell(cell.Text))
			}
		}
		a.updateTitle()
	})
}

func (a *App) updateTitle() {
	a.mu.RLock()
	resource := a.currentGVR.Resource
	a.mu.RUnlock()
	if resource == "" {
		resource = "resources"
	}
	title := fmt.Sprintf(" astrotui [%s/%s] %s ", a.options.Client.Context, a.options.Client.Namespace, resource)
	a.mu.RLock()
	filter := a.filter
	a.mu.RUnlock()
	if filter != "" {
		title += fmt.Sprintf(" filter=%q ", filter)
	}
	a.table.SetTitle(title)
}

func (a *App) switchResource(ctx context.Context, gvr schema.GroupVersionResource) bool {
	renderer := rendererForGVR(gvr)
	if renderer == nil {
		a.showMessage("Unknown resource", gvr.String())
		return false
	}
	watchCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is retained to stop the active resource watcher on the next switch.
	a.mu.Lock()
	if a.resourceCancel != nil {
		a.resourceCancel()
	}
	a.resourceCancel = cancel
	a.currentGVR = gvr
	a.renderer = renderer
	a.objects = nil
	a.visibleObjects = nil
	a.filter = ""
	a.sortCol = 0
	a.sortDesc = false
	cache := a.cache
	client := a.options.Client
	a.mu.Unlock()
	cache.Ensure(gvr)
	a.drawHeader(renderer)
	events := cache.Subscribe(gvr)
	go func() {
		cache.WaitForSync(watchCtx, gvr)
		a.refresh(cache, client, gvr, renderer)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-events:
				a.refresh(cache, client, gvr, renderer)
			}
		}
	}()
	return true
}

func rendererForGVR(gvr schema.GroupVersionResource) views.Renderer {
	if gvr.Resource == "" || gvr.Version == "" {
		return nil
	}
	if renderer, ok := views.Builtins()[gvr]; ok {
		return renderer
	}
	return views.GenericRenderer{Resource: gvr}
}

func (a *App) showHelp() {
	modal := tview.NewModal().
		SetText(HelpText(a.actionCatalog())).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) { a.app.SetRoot(a.table, true) })
	a.app.SetRoot(modal, false)
}

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

func (a *App) showFilter() {
	a.mu.RLock()
	filter := a.filter
	a.mu.RUnlock()
	input := tview.NewInputField().
		SetLabel("Filter: ").
		SetText(filter).
		SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.mu.Lock()
			a.filter = input.GetText()
			a.mu.Unlock()
			if a.renderer != nil {
				a.renderCurrent()
			}
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showCommand(ctx context.Context) {
	input := tview.NewInputField().
		SetLabel(":").
		SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			command, ok := parseCommand(input.GetText())
			if !ok {
				a.showMessage("Unknown command", ":"+input.GetText())
				return
			}
			switch command.Kind {
			case "resource":
				gvr, ok := resourceCommand(command.Value)
				if !ok {
					a.showMessage("Unknown command", ":"+input.GetText())
					return
				}
				a.switchResource(ctx, gvr)
			case "namespace":
				a.switchNamespace(command.Value)
			case "port-forward-manager":
				a.showPortForwardManager()
				return
			}
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) switchNamespace(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}
	if a.options.Client == nil {
		a.options.Client = &cluster.Client{}
	}
	a.options.Client.Namespace = namespace
	a.renderCurrent()
}

func (a *App) switchContext(ctx context.Context, contextName string) bool {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return false
	}
	namespace := ""
	if a.options.Client != nil {
		namespace = a.options.Client.Namespace
	}
	client, err := a.clientFactory()(ctx, cluster.ClientOptions{Context: contextName, Namespace: namespace})
	if err != nil {
		a.showMessage("Context switch failed", err.Error())
		return false
	}
	newCache := cluster.NewResourceCache(client.Dynamic, 30*time.Minute)
	newCache.Start(ctx)
	a.mu.Lock()
	if a.resourceCancel != nil {
		a.resourceCancel()
		a.resourceCancel = nil
	}
	a.options.Client = client
	a.cache = newCache
	gvr := a.currentGVR
	a.mu.Unlock()
	if gvr.Resource == "" {
		gvr = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	}
	if !a.switchResource(ctx, gvr) {
		return false
	}
	a.app.SetRoot(a.table, true)
	return true
}

func (a *App) clientFactory() func(context.Context, cluster.ClientOptions) (*cluster.Client, error) {
	if a.options.ClientFactory != nil {
		return a.options.ClientFactory
	}
	return cluster.NewClient
}

func resolveSkin(options Options) uitheme.Skin {
	if options.Skin.Name != "" {
		return options.Skin
	}
	name := options.Config.Theme
	if name == "" {
		name = "dark"
	}
	if skin, ok := uitheme.Defaults()[name]; ok {
		return skin
	}
	return uitheme.Defaults()["dark"]
}

func skinColor(value string, fallback tcell.Color) tcell.Color {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	color := tcell.GetColor(value)
	if color == tcell.ColorDefault {
		return fallback
	}
	return color
}

func (a *App) showDelete() {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	req := deleteRequest(a.renderer.GVR(), obj)
	if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Delete) {
		a.showTypedDelete(req)
		return
	}
	a.confirmDelete(req, "")
}

func (a *App) showEdit() {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	req := actionRequest("edit", a.renderer.GVR(), obj)
	if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Edit) {
		input := tview.NewInputField().
			SetLabel(fmt.Sprintf("Type %s to edit %s/%s: ", a.options.Client.Context, req.Kind, req.ResourceName)).
			SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmEdit(obj, input.GetText())
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmEdit(obj, "")
}

func (a *App) confirmEdit(obj *unstructured.Unstructured, confirmText string) {
	if err := safety.Authorize(a.options.Config, a.options.Client.Context, safety.Edit, confirmText); err != nil {
		a.showMessage("Edit failed", err.Error())
		return
	}
	var err error
	run := func() {
		_, err = actions.EditResource(context.Background(), a.options.Client.Dynamic, a.renderer.GVR(), obj, a.options.Editor)
	}
	if !a.app.Suspend(run) {
		run()
	}
	if err != nil {
		var conflict *actions.EditConflictError
		if errors.As(err, &conflict) {
			a.showMessage("Edit conflict", conflict.Diff)
			return
		}
		a.showMessage("Edit failed", err.Error())
		return
	}
	a.showMessage("Edited", fmt.Sprintf("edited %s/%s", fallbackKind(a.renderer.GVR().Resource), obj.GetName()))
}

func (a *App) showLogsPlan() {
	if pod, ok := a.selectedPod(); ok && len(logs.ContainerNames(pod)) > 1 {
		a.showLogContainerPicker(pod)
		return
	}
	targets, err := a.selectedLogTargets(context.Background())
	if err != nil {
		a.showMessage("Logs unavailable", err.Error())
		return
	}
	a.showLogsView(targets)
}

func (a *App) showLogsView(targets []logs.PodLogTarget) {
	ctx, cancel := context.WithCancel(context.Background())
	buffer := logs.NewBuffer(10_000)
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	wrap := true
	view.SetWrap(wrap).SetWordWrap(true)
	view.SetTitle(" Logs ").SetBorder(true)
	filter := ""
	search := logSearchState{}
	gotoPending := false
	render := func() {
		view.SetText(renderLogEntries(buffer.Filter(filter), true))
	}
	render()
	streamer := a.logStreamer()
	for _, target := range targets {
		target := target
		go func() {
			writer := newLogLineWriter(target.Pod, buffer, func() {
				a.app.QueueUpdateDraw(render)
			})
			if err := streamer(ctx, target, logs.RequestOptions{Follow: true}, writer); err != nil {
				buffer.Append(target.Pod, "stream error: "+err.Error(), time.Now())
				a.app.QueueUpdateDraw(render)
			}
		}()
	}
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			cancel()
			a.app.SetRoot(a.table, true)
			return nil
		case event.Rune() == '/':
			a.showLogFilter(view, buffer, &filter)
			return nil
		case event.Key() == tcell.KeyCtrlR:
			a.showLogRegex(view, buffer, &search)
			return nil
		case event.Rune() == 'n':
			if row, ok := search.Next(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Rune() == 'N':
			if row, ok := search.Prev(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Rune() == 's':
			a.showLogSave(view, buffer, filter)
			return nil
		case event.Rune() == 'g':
			gotoPending = true
			view.SetTitle(" Logs: g e events | g o owner | g a AI ")
			return nil
		case gotoPending && event.Rune() == 'e':
			gotoPending = false
			a.jumpLogEvents(view, buffer, filter)
			return nil
		case gotoPending && event.Rune() == 'o':
			gotoPending = false
			a.jumpLogOwner(context.Background(), view, buffer, filter)
			return nil
		case gotoPending && event.Rune() == 'a':
			gotoPending = false
			a.showLogAISummary(view, buffer, filter)
			return nil
		case event.Rune() == 'w':
			toggleLogWrap(view, &wrap)
			return nil
		case event.Key() == tcell.KeyCtrlE:
			a.showLogAIChat(view, buffer, filter)
			return nil
		case event.Rune() == 'a':
			a.showLogAISummary(view, buffer, filter)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) jumpLogEvents(view *tview.TextView, buffer *logs.Buffer, filter string) {
	entry, ok := selectedLogEntry(view, buffer, filter)
	if !ok || strings.TrimSpace(entry.Pod) == "" {
		view.SetText("No pod log line selected.")
		return
	}
	a.switchResourceFiltered(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "events"}, entry.Pod)
	a.app.SetRoot(a.table, true)
}

func (a *App) jumpLogOwner(ctx context.Context, view *tview.TextView, buffer *logs.Buffer, filter string) {
	entry, ok := selectedLogEntry(view, buffer, filter)
	if !ok || strings.TrimSpace(entry.Pod) == "" {
		view.SetText("No pod log line selected.")
		return
	}
	pod, err := a.podForLogEntry(ctx, entry)
	if err != nil {
		view.SetText("Owner jump unavailable: " + err.Error())
		return
	}
	if len(pod.OwnerReferences) == 0 {
		view.SetText("Owner jump unavailable: pod has no owner references")
		return
	}
	owner := pod.OwnerReferences[0]
	gvr, ok := ownerGVR(owner.Kind)
	if !ok {
		view.SetText("Owner jump unavailable: unsupported owner kind " + owner.Kind)
		return
	}
	a.switchResourceFiltered(ctx, gvr, owner.Name)
	a.app.SetRoot(a.table, true)
}

func (a *App) switchResourceFiltered(ctx context.Context, gvr schema.GroupVersionResource, filter string) bool {
	if !a.switchResource(ctx, gvr) {
		return false
	}
	a.mu.Lock()
	a.filter = filter
	a.mu.Unlock()
	a.updateTitle()
	return true
}

func selectedLogEntry(view *tview.TextView, buffer *logs.Buffer, filter string) (logs.Entry, bool) {
	entries := buffer.Filter(filter)
	if len(entries) == 0 {
		return logs.Entry{}, false
	}
	row, _ := view.GetScrollOffset()
	if row < 0 {
		row = 0
	}
	if row >= len(entries) {
		row = len(entries) - 1
	}
	return entries[row], true
}

func (a *App) podForLogEntry(ctx context.Context, entry logs.Entry) (corev1.Pod, error) {
	if pod, ok := a.selectedPod(); ok && pod.Name == entry.Pod {
		return pod, nil
	}
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return corev1.Pod{}, fmt.Errorf("kubernetes clientset is required")
	}
	namespace := a.options.Client.Namespace
	if entry.Pod != "" {
		targets, err := a.selectedLogTargets(ctx)
		if err == nil {
			for _, target := range targets {
				if target.Pod == entry.Pod && target.Namespace != "" {
					namespace = target.Namespace
					break
				}
			}
		}
	}
	pod, err := a.options.Client.Clientset.CoreV1().Pods(namespace).Get(ctx, entry.Pod, metav1.GetOptions{})
	if err != nil {
		return corev1.Pod{}, err
	}
	return *pod, nil
}

func ownerGVR(kind string) (schema.GroupVersionResource, bool) {
	switch kind {
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true
	case "StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true
	case "DaemonSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true
	case "ReplicaSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, true
	case "Job":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, true
	case "CronJob":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, true
	default:
		return schema.GroupVersionResource{}, false
	}
}

func (a *App) showLogContainerPicker(pod corev1.Pod) {
	list := tview.NewList()
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.showLogsView([]logs.PodLogTarget{logTargetForPodContainer(pod, container)})
		})
	}
	list.SetTitle(" Select log container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func logTargetForPodContainer(pod corev1.Pod, container string) logs.PodLogTarget {
	return logs.PodLogTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container}
}

func toggleLogWrap(view *tview.TextView, wrap *bool) {
	*wrap = !*wrap
	view.SetWrap(*wrap)
}

func (a *App) showLogFilter(view *tview.TextView, buffer *logs.Buffer, filter *string) {
	input := tview.NewInputField().
		SetLabel("Log filter: ").
		SetText(*filter).
		SetFieldWidth(50)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			*filter = input.GetText()
			view.SetText(renderLogEntries(buffer.Filter(*filter), true))
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogRegex(view *tview.TextView, buffer *logs.Buffer, search *logSearchState) {
	input := tview.NewInputField().
		SetLabel("Regex: ").
		SetFieldWidth(50)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			matches, err := buffer.RegexMatches(input.GetText())
			if err != nil {
				view.SetText("regex error: " + err.Error())
			} else {
				*search = newLogSearchState(matches)
				view.SetText(fmt.Sprintf("%d matches\n\n%s", len(matches), renderLogEntries(buffer.Entries(), true)))
			}
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogSave(view *tview.TextView, buffer *logs.Buffer, filter string) {
	input := tview.NewInputField().
		SetLabel("Save logs: ").
		SetFieldWidth(60)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			path := strings.TrimSpace(input.GetText())
			if path != "" {
				if err := buffer.Save(path, filter); err != nil {
					view.SetText("save error: " + err.Error())
				} else {
					view.SetText(fmt.Sprintf("saved logs to %s\n\n%s", path, renderLogEntries(buffer.Filter(filter), true)))
				}
			}
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogAISummary(view *tview.TextView, buffer *logs.Buffer, filter string) {
	if a.options.AIProvider == nil {
		view.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	prompt := logSummaryContext(buffer.Filter(filter), 100)
	if strings.TrimSpace(prompt) == "" {
		view.SetText("No log lines available to summarize")
		return
	}
	a.streamAIResponse(view, ai.LogSummaryRequest(prompt))
}

func (a *App) showLogAIChat(logView *tview.TextView, buffer *logs.Buffer, filter string) {
	if a.options.AIProvider == nil {
		logView.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	contextText := logSummaryContext(buffer.Filter(filter), 100)
	if strings.TrimSpace(contextText) == "" {
		logView.SetText("No log lines available for AI.")
		return
	}
	output := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetText("Ask a follow-up about these logs.")
	output.SetTitle(" AI logs ").SetBorder(true)
	input := tview.NewInputField().SetLabel("Ask: ").SetFieldWidth(0)
	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			question := strings.TrimSpace(input.GetText())
			if question == "" {
				return
			}
			input.SetText("")
			a.streamAIResponse(output, ai.LogQuestionRequest(contextText, question))
		case tcell.KeyEsc:
			a.app.SetRoot(logView, true)
		}
	})
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(output, 0, 1, false).
		AddItem(input, 1, 0, true)
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(logView, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(flex, true)
}

func (a *App) showFailureAI() {
	if a.options.AIProvider == nil {
		a.showMessage("AI disabled", "Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	pod, ok := a.selectedPod()
	if !ok {
		a.showMessage("AI explain failure unavailable", "select a failing pod")
		return
	}
	if reason := podFailureReason(pod); reason == "" {
		a.showMessage("AI explain failure unavailable", fmt.Sprintf("pod/%s is not currently failing", pod.Name))
		return
	}
	spec, status, events, err := a.podFailureContext(context.Background(), pod)
	if err != nil {
		a.showMessage("AI failed", err.Error())
		return
	}
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetText("Asking AI...")
	view.SetTitle(" AI failure analysis ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
	a.streamAIResponse(view, ai.FailureRequest(spec, status, events))
}

func (a *App) podFailureContext(ctx context.Context, pod corev1.Pod) (string, string, string, error) {
	spec, err := yaml.Marshal(pod.Spec)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal pod spec: %w", err)
	}
	status, err := yaml.Marshal(pod.Status)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal pod status: %w", err)
	}
	return string(spec), string(status), a.podEventSummary(ctx, pod), nil
}

func (a *App) podEventSummary(ctx context.Context, pod corev1.Pod) string {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return "No recent events available."
	}
	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.name", pod.Name),
		fields.OneTermEqualSelector("involvedObject.kind", "Pod"),
	)
	list, err := a.options.Client.Clientset.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil || len(list.Items) == 0 {
		return "No recent events available."
	}
	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].LastTimestamp.Before(&list.Items[j].LastTimestamp)
	})
	lines := make([]string, 0, len(list.Items))
	for _, event := range list.Items {
		lines = append(lines, fmt.Sprintf("%s %s %s", event.Type, event.Reason, event.Message))
	}
	return strings.Join(lines, "\n")
}

func podFailureReason(pod corev1.Pod) string {
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			return status.State.Waiting.Reason
		}
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			if status.State.Terminated.Reason != "" {
				return status.State.Terminated.Reason
			}
			return fmt.Sprintf("exit code %d", status.State.Terminated.ExitCode)
		}
	}
	switch pod.Status.Phase {
	case corev1.PodFailed, corev1.PodPending:
		return string(pod.Status.Phase)
	default:
		return ""
	}
}

func (a *App) logStreamer() func(context.Context, logs.PodLogTarget, logs.RequestOptions, io.Writer) error {
	if a.options.LogStreamer != nil {
		return a.options.LogStreamer
	}
	return func(ctx context.Context, target logs.PodLogTarget, opts logs.RequestOptions, writer io.Writer) error {
		if a.options.Client == nil || a.options.Client.Clientset == nil {
			return fmt.Errorf("kubernetes clientset is required for log streaming")
		}
		return logs.StreamPodLogs(ctx, a.options.Client.Clientset, target, opts, writer)
	}
}

func renderLogEntries(entries []logs.Entry, prefixPod bool) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, formatLogEntryForView(entry, prefixPod))
	}
	return strings.Join(lines, "\n")
}

func formatLogEntryForView(entry logs.Entry, prefixPod bool) string {
	if !prefixPod || entry.Pod == "" {
		return logs.FormatEntry(entry, prefixPod)
	}
	colored := fmt.Sprintf("[%s]%s[-]", logColor(entry.Color), logs.PrefixPod(entry.Pod, entry.Line))
	if !entry.Time.IsZero() {
		return fmt.Sprintf("%s %s", entry.Time.Format(time.RFC3339), colored)
	}
	return colored
}

func logColor(index int) string {
	colors := []string{"red", "green", "yellow", "blue", "purple", "aqua", "orange", "teal", "maroon", "lime", "fuchsia", "navy", "olive", "silver", "white", "gray"}
	if index < 0 {
		index = -index
	}
	return colors[index%len(colors)]
}

func logSummaryContext(entries []logs.Entry, limit int) string {
	if limit <= 0 || len(entries) <= limit {
		return renderLogEntries(entries, true)
	}
	return renderLogEntries(entries[len(entries)-limit:], true)
}

type logLineWriter struct {
	pod      string
	buffer   *logs.Buffer
	onAppend func()
	pending  string
}

type logSearchState struct {
	matches []int
	current int
}

func newLogSearchState(matches []int) logSearchState {
	return logSearchState{matches: append([]int(nil), matches...), current: -1}
}

func (s *logSearchState) Next() (int, bool) {
	if len(s.matches) == 0 {
		return 0, false
	}
	s.current = (s.current + 1) % len(s.matches)
	return s.matches[s.current], true
}

func (s *logSearchState) Prev() (int, bool) {
	if len(s.matches) == 0 {
		return 0, false
	}
	if s.current < 0 {
		s.current = 0
	}
	s.current = (s.current - 1 + len(s.matches)) % len(s.matches)
	return s.matches[s.current], true
}

func newLogLineWriter(pod string, buffer *logs.Buffer, onAppend func()) *logLineWriter {
	return &logLineWriter{pod: pod, buffer: buffer, onAppend: onAppend}
}

func (w *logLineWriter) Write(data []byte) (int, error) {
	w.pending += string(data)
	scanner := bufio.NewScanner(strings.NewReader(w.pending))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if strings.HasSuffix(w.pending, "\n") {
		w.pending = ""
	} else if len(lines) > 0 {
		w.pending = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		w.buffer.Append(w.pod, line, time.Now())
		if w.onAppend != nil {
			w.onAppend()
		}
	}
	return len(data), scanner.Err()
}

func (a *App) showExecPlan() {
	currentContext := ""
	if a.options.Client != nil {
		currentContext = a.options.Client.Context
	}
	if safety.RequiresConfirmation(a.options.Config, currentContext, safety.Exec) {
		input := tview.NewInputField().
			SetLabel(fmt.Sprintf("Type %s to exec into pod: ", currentContext)).
			SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmExecPlan(input.GetText())
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmExecPlan("")
}

func (a *App) confirmExecPlan(confirmText string) {
	pod, ok := a.selectedPod()
	if ok && actions.NeedsContainerPicker(pod) {
		a.showExecContainerPicker(confirmText, pod)
		return
	}
	a.confirmExecPlanForContainer(confirmText, "")
}

func (a *App) confirmExecPlanForContainer(confirmText, container string) {
	targets, lines, err := a.execTargetsAndPlan(confirmText, container)
	if err != nil {
		a.showMessage("Exec unavailable", err.Error())
		return
	}
	if len(targets) > 0 && a.canRunExec() {
		var runErr error
		run := func() {
			runErr = runExecFallback(context.Background(), a.execRunner(), targets)
		}
		if !a.app.Suspend(run) {
			run()
		}
		if runErr != nil {
			a.showMessage("Exec failed", runErr.Error())
			return
		}
		a.showMessage("Exec", "session ended")
		return
	}
	a.showMessage("Exec", strings.Join(lines, "\n"))
}

func runExecFallback(ctx context.Context, runner func(context.Context, actions.ExecTarget) error, targets []actions.ExecTarget) error {
	var lastErr error
	for _, target := range targets {
		if err := runner(ctx, target); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (a *App) execPlanLines(confirmText string) ([]string, error) {
	_, lines, err := a.execTargetsAndPlan(confirmText, "")
	return lines, err
}

func (a *App) execTargetsAndPlan(confirmText, preferredContainer string) ([]actions.ExecTarget, []string, error) {
	currentContext := ""
	if a.options.Client != nil {
		currentContext = a.options.Client.Context
	}
	if err := safety.Authorize(a.options.Config, currentContext, safety.Exec, confirmText); err != nil {
		return nil, nil, err
	}
	pod, ok := a.selectedPod()
	if !ok {
		return nil, nil, fmt.Errorf("exec is currently implemented for pod selections")
	}
	targets, err := actions.ExecTargets(pod, preferredContainer)
	if err != nil {
		return nil, nil, err
	}
	var lines []string
	for _, target := range targets {
		lines = append(lines, fmt.Sprintf("kubectl exec -n %s %s -c %s -- %s", target.Namespace, target.Pod, target.Container, strings.Join(target.Command, " ")))
	}
	return targets, lines, nil
}

func (a *App) showExecContainerPicker(confirmText string, pod corev1.Pod) {
	list := tview.NewList().ShowSecondaryText(false)
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.confirmExecPlanForContainer(confirmText, container)
		})
	}
	list.SetTitle(" Select container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) canRunExec() bool {
	return a.options.ExecRunner != nil || (a.options.Client != nil && a.options.Client.Config != nil && a.options.Client.Clientset != nil)
}

func (a *App) execRunner() func(context.Context, actions.ExecTarget) error {
	if a.options.ExecRunner != nil {
		return a.options.ExecRunner
	}
	return func(ctx context.Context, target actions.ExecTarget) error {
		return actions.RunExec(ctx, a.options.Client.Config, a.options.Client.Clientset, target, os.Stdin, os.Stdout, os.Stderr)
	}
}

func (a *App) showPortForwardPlan() {
	spec, ok := a.defaultPortForwardSpec()
	if !ok {
		return
	}
	input := tview.NewInputField().
		SetLabel("Port-forward local:remote: ").
		SetText(fmt.Sprintf("%d:%d", spec.Local, spec.Remote)).
		SetFieldWidth(20)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			updated, err := parsePortForwardSpec(input.GetText(), spec)
			if err != nil {
				a.showMessage("Port-forward unavailable", err.Error())
				return
			}
			a.startPortForwardSpec(updated)
			return
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) defaultPortForwardSpec() (actions.PortForwardSpec, bool) {
	obj := a.selectedObject()
	if obj == nil {
		return actions.PortForwardSpec{}, false
	}
	var specs []actions.PortForwardSpec
	switch obj.GetKind() {
	case "Pod":
		pod, ok := toTyped[corev1.Pod](obj)
		if ok {
			specs = actions.DefaultPodForwards(pod)
		}
	case "Service":
		service, ok := toTyped[corev1.Service](obj)
		if ok {
			specs = actions.DefaultServiceForwards(service)
		}
	default:
		a.showMessage("Port-forward unavailable", "port-forward is currently implemented for pod and service selections")
		return actions.PortForwardSpec{}, false
	}
	if len(specs) == 0 {
		a.showMessage("Port-forward unavailable", fmt.Sprintf("%s/%s has no declared ports", obj.GetKind(), obj.GetName()))
		return actions.PortForwardSpec{}, false
	}
	return specs[0], true
}

func parsePortForwardSpec(input string, base actions.PortForwardSpec) (actions.PortForwardSpec, error) {
	parts := strings.Split(strings.TrimSpace(input), ":")
	if len(parts) != 2 {
		return actions.PortForwardSpec{}, fmt.Errorf("expected local:remote ports")
	}
	local, err := strconv.Atoi(parts[0])
	if err != nil || local <= 0 {
		return actions.PortForwardSpec{}, fmt.Errorf("local port must be a positive integer")
	}
	remote, err := strconv.Atoi(parts[1])
	if err != nil || remote <= 0 {
		return actions.PortForwardSpec{}, fmt.Errorf("remote port must be a positive integer")
	}
	base.Local = local
	base.Remote = remote
	return base, nil
}

func (a *App) startPortForwardSpec(spec actions.PortForwardSpec) {
	manager := a.options.PortForwards
	if manager == nil {
		manager = actions.NewPortForwardManager()
		a.options.PortForwards = manager
	}
	ctx, cancel := context.WithCancel(context.Background())
	go a.runPortForward(ctx, spec)
	manager.Add(actions.NewPortForward(spec, cancel))
	a.showMessage("Port-forward", fmt.Sprintf("%s %d:%d", spec.Resource, spec.Local, spec.Remote))
}

func (a *App) runPortForward(ctx context.Context, spec actions.PortForwardSpec) {
	if a.options.PortForwardRun != nil {
		_ = a.options.PortForwardRun(ctx, spec)
		return
	}
	if a.options.Client == nil || a.options.Client.Config == nil || a.options.Client.Clientset == nil {
		<-ctx.Done()
		return
	}
	_ = actions.RunPortForward(ctx, a.options.Client.Config, a.options.Client.Clientset, spec, io.Discard, io.Discard)
}

func (a *App) showPortForwardManager() {
	manager := a.options.PortForwards
	if manager == nil {
		manager = actions.NewPortForwardManager()
		a.options.PortForwards = manager
	}
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" Port-forwards ").SetBorder(true)
	renderPortForwardRows(table, manager.List())
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			a.app.SetRoot(a.table, true)
			return nil
		case event.Key() == tcell.KeyCtrlK || event.Rune() == 'k' || event.Rune() == 'd':
			if killSelectedPortForward(table, manager) {
				renderPortForwardRows(table, manager.List())
			}
			return nil
		}
		return event
	})
	a.app.SetRoot(table, true)
}

func renderPortForwardRows(table *tview.Table, forwards []actions.PortForward) {
	table.Clear()
	headers := []string{"ID", "NAMESPACE", "RESOURCE", "LOCAL", "REMOTE", "SINCE"}
	for col, header := range headers {
		table.SetCell(0, col, tview.NewTableCell(header).SetTextColor(tcell.ColorAqua).SetSelectable(false))
	}
	sort.SliceStable(forwards, func(i, j int) bool { return forwards[i].ID < forwards[j].ID })
	for row, forward := range forwards {
		values := []string{
			forward.ID,
			forward.Namespace,
			forward.Resource,
			fmt.Sprint(forward.Local),
			fmt.Sprint(forward.Remote),
			time.Since(forward.Since).Round(time.Second).String(),
		}
		for col, value := range values {
			table.SetCell(row+1, col, tview.NewTableCell(value))
		}
	}
	if len(forwards) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("No active port-forwards"))
	}
}

func portForwardIDAt(table *tview.Table, forwards []actions.PortForward) string {
	row, _ := table.GetSelection()
	index := row - 1
	if index < 0 || index >= len(forwards) {
		return ""
	}
	sort.SliceStable(forwards, func(i, j int) bool { return forwards[i].ID < forwards[j].ID })
	return forwards[index].ID
}

func killSelectedPortForward(table *tview.Table, manager *actions.PortForwardManager) bool {
	if manager == nil {
		return false
	}
	if id := portForwardIDAt(table, manager.List()); id != "" {
		return manager.Kill(id)
	}
	return false
}

func (a *App) selectedPod() (corev1.Pod, bool) {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Pod" {
		return corev1.Pod{}, false
	}
	return toTyped[corev1.Pod](obj)
}

func (a *App) selectedLogTargets(ctx context.Context) ([]logs.PodLogTarget, error) {
	obj := a.selectedObject()
	if obj == nil {
		return nil, fmt.Errorf("no resource selected")
	}
	switch obj.GetKind() {
	case "Pod":
		pod, ok := toTyped[corev1.Pod](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode pod %s", obj.GetName())
		}
		return logs.TargetsForPods([]corev1.Pod{pod}), nil
	case "Deployment":
		deployment, ok := toTyped[appsv1.Deployment](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode deployment %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, deployment.Namespace, deployment.Spec.Selector)
	case "StatefulSet":
		statefulSet, ok := toTyped[appsv1.StatefulSet](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode statefulset %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, statefulSet.Namespace, statefulSet.Spec.Selector)
	default:
		return nil, fmt.Errorf("logs are currently implemented for pod and workload selections")
	}
}

func (a *App) logTargetsForSelector(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]logs.PodLogTarget, error) {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return nil, fmt.Errorf("kubernetes clientset is required for workload logs")
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}
	if parsed.Empty() {
		return nil, fmt.Errorf("workload has an empty pod selector")
	}
	pods, err := logs.PodsForSelector(ctx, a.options.Client.Clientset, namespace, parsed)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods match selector %s", parsed.String())
	}
	return logs.TargetsForPods(pods), nil
}

func toTyped[T any](obj *unstructured.Unstructured) (T, bool) {
	var out T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &out); err != nil {
		return out, false
	}
	return out, true
}

func (a *App) showTypedDelete(req actions.Request) {
	input := tview.NewInputField().
		SetLabel(fmt.Sprintf("Type %s to delete %s/%s: ", a.options.Client.Context, req.Kind, req.ResourceName)).
		SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.confirmDelete(req, input.GetText())
			return
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) confirmDelete(req actions.Request, confirmText string) {
	req.ConfirmText = confirmText
	a.runAction(req, "Deleted", "Delete failed")
}

func (a *App) showScale() {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	req := actionRequest("scale", a.renderer.GVR(), obj)
	if !scalableKind(req.Kind) {
		a.showMessage("Scale unavailable", fmt.Sprintf("scale is not implemented for %s", req.Kind))
		return
	}
	input := tview.NewInputField().
		SetLabel(fmt.Sprintf("Replicas for %s/%s: ", req.Kind, req.ResourceName)).
		SetFieldWidth(8)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			req.Args = map[string]string{"replicas": input.GetText()}
			if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Scale) {
				a.showTypedAction(req, safety.Scale, "Scale", "Scale failed")
				return
			}
			a.runAction(req, "Scaled", "Scale failed")
			return
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showRestart() {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	req := actionRequest("restart", a.renderer.GVR(), obj)
	if !restartableKind(req.Kind) {
		a.showMessage("Restart unavailable", fmt.Sprintf("restart is not implemented for %s", req.Kind))
		return
	}
	if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Restart) {
		a.showTypedAction(req, safety.Restart, "Restarted", "Restart failed")
		return
	}
	a.runAction(req, "Restarted", "Restart failed")
}

func scalableKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet"
}

func restartableKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet"
}

func (a *App) showTypedAction(req actions.Request, verb safety.Verb, successTitle, failureTitle string) {
	input := tview.NewInputField().
		SetLabel(fmt.Sprintf("Type %s to %s %s/%s: ", a.options.Client.Context, verb, req.Kind, req.ResourceName)).
		SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			req.ConfirmText = input.GetText()
			a.runAction(req, successTitle, failureTitle)
			return
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) runAction(req actions.Request, successTitle, failureTitle string) {
	executor := actions.Executor{
		Clientset:      a.options.Client.Clientset,
		Dynamic:        a.options.Client.Dynamic,
		Config:         a.options.Config,
		CurrentContext: a.options.Client.Context,
	}
	result, err := executor.Run(context.Background(), req)
	if err != nil {
		a.showMessage(failureTitle, err.Error())
		return
	}
	a.showMessage(successTitle, result.Message)
}

func (a *App) showMessage(title, body string) {
	modal := tview.NewModal().
		SetText(body).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) { a.app.SetRoot(a.table, true) })
	modal.SetTitle(" " + title + " ")
	a.app.SetRoot(modal, false)
}

func (a *App) safe(fn func()) bool {
	return SafeWithReport(a.crashes, fn, func(report CrashReport) {
		body := "A handler panic was recovered."
		if report.Path != "" {
			body += "\nCrash log: " + report.Path
		}
		if report.Err != nil {
			body += "\nCrash log error: " + report.Err.Error()
		}
		a.showMessage("Recovered panic", body)
	})
}

func (a *App) setSort(col int) {
	if a.renderer == nil || col >= len(a.renderer.Header()) {
		return
	}
	a.mu.Lock()
	if a.sortCol == col {
		a.sortDesc = !a.sortDesc
	} else {
		a.sortCol = col
		a.sortDesc = false
	}
	a.mu.Unlock()
	a.renderCurrent()
}

func (a *App) renderCurrent() {
	if a.renderer == nil {
		return
	}
	a.mu.RLock()
	objects := append([]*unstructured.Unstructured(nil), a.objects...)
	filter, sortCol, sortDesc := a.filter, a.sortCol, a.sortDesc
	a.mu.RUnlock()
	rows, visible := buildRows(a.renderer, objects, filter, sortCol, sortDesc)
	a.mu.Lock()
	a.visibleObjects = visible
	a.mu.Unlock()
	a.table.Clear()
	a.drawHeader(a.renderer)
	for row, cells := range rows {
		for col, cell := range cells {
			a.table.SetCell(row+1, col, tview.NewTableCell(cell.Text))
		}
	}
	a.updateTitle()
}

func (a *App) showDetail() {
	a.showDetailFor(a.selectedObject())
}

func (a *App) showDetailFor(obj *unstructured.Unstructured) {
	if obj == nil || a.renderer == nil {
		return
	}
	detail := a.renderer.Detail(obj)
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetText(detail.Body)
	view.SetTitle(" " + detail.Title + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		if event.Key() == tcell.KeyCtrlE || event.Rune() == 'a' {
			a.showManifestAI(view, detail.Body)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) showManifestAI(view *tview.TextView, manifest string) {
	if a.options.AIProvider == nil {
		view.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	a.streamAIResponse(view, ai.ManifestRequest(manifest))
}

func (a *App) streamAIResponse(view *tview.TextView, req ai.Request) {
	view.SetText("Asking AI...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		chunks, err := a.options.AIProvider.Stream(ctx, req)
		if err != nil {
			a.app.QueueUpdateDraw(func() { view.SetText("AI failed: " + err.Error()) })
			return
		}
		var b strings.Builder
		for chunk := range chunks {
			if chunk.Err != nil {
				errText := chunk.Err.Error()
				a.app.QueueUpdateDraw(func() { view.SetText("AI failed: " + errText) })
				return
			}
			if chunk.Text != "" {
				b.WriteString(chunk.Text)
				text := b.String()
				a.app.QueueUpdateDraw(func() { view.SetText(text) })
			}
			if chunk.Done {
				return
			}
		}
	}()
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

func (a *App) namespaceNames() []string {
	names := map[string]struct{}{}
	if a.options.Client != nil && a.options.Client.Namespace != "" {
		names[a.options.Client.Namespace] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	discovered, err := cluster.ListNamespaces(ctx, a.options.Client)
	if err == nil {
		for _, name := range discovered {
			names[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func namespaceFor(client *cluster.Client, gvr schema.GroupVersionResource) string {
	if clusterScoped(gvr.Resource) {
		return ""
	}
	if client == nil {
		return ""
	}
	return client.Namespace
}

func HelpText(catalog []actions.Action) string {
	lines := []string{"Available actions"}
	for _, action := range catalog {
		lines = append(lines, fmt.Sprintf("%s  %s - %s", action.Key, action.Name, action.Description))
	}
	lines = append(lines, "Ctrl+P  palette - Search resources, actions, contexts, namespaces, plugins")
	lines = append(lines, ":  command - Switch resource view (for example :pods, :deployments, :services)")
	lines = append(lines, "/  filter - Filter visible rows")
	lines = append(lines, "Shift+1..9  sort - Sort by column")
	lines = append(lines, "?  help - Show this overlay")
	return strings.Join(lines, "\n")
}

func deleteRequest(gvr schema.GroupVersionResource, obj *unstructured.Unstructured) actions.Request {
	return actionRequest("delete", gvr, obj)
}

func actionRequest(name string, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) actions.Request {
	kind := obj.GetKind()
	if kind == "" {
		kind = fallbackKind(gvr.Resource)
	}
	return actions.Request{
		Name:         name,
		Kind:         kind,
		Namespace:    obj.GetNamespace(),
		ResourceName: obj.GetName(),
		GVR:          gvr,
	}
}

func fallbackKind(resource string) string {
	resource = strings.TrimSuffix(resource, "s")
	if resource == "" {
		return ""
	}
	return strings.ToUpper(resource[:1]) + resource[1:]
}

func buildRows(renderer views.Renderer, objects []*unstructured.Unstructured, filter string, sortCol int, sortDesc bool) ([][]views.Cell, []*unstructured.Unstructured) {
	return BuildRows(renderer, objects, filter, sortCol, sortDesc)
}

func BuildRows(renderer views.Renderer, objects []*unstructured.Unstructured, filter string, sortCol int, sortDesc bool) ([][]views.Cell, []*unstructured.Unstructured) {
	type rowObject struct {
		row []views.Cell
		obj *unstructured.Unstructured
	}
	pairs := make([]rowObject, 0, len(objects))
	query := strings.ToLower(strings.TrimSpace(filter))
	for _, obj := range objects {
		row := renderer.Row(obj)
		if query != "" && !rowMatches(row, query) {
			continue
		}
		pairs = append(pairs, rowObject{row: row, obj: obj})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if sortCol >= len(pairs[i].row) || sortCol >= len(pairs[j].row) {
			return false
		}
		if sortDesc {
			return pairs[i].row[sortCol].Text > pairs[j].row[sortCol].Text
		}
		return pairs[i].row[sortCol].Text < pairs[j].row[sortCol].Text
	})
	rows := make([][]views.Cell, len(pairs))
	visible := make([]*unstructured.Unstructured, len(pairs))
	for i, pair := range pairs {
		rows[i] = pair.row
		visible[i] = pair.obj
	}
	return rows, visible
}

func rowMatches(row []views.Cell, query string) bool {
	for _, cell := range row {
		if strings.Contains(strings.ToLower(cell.Text), query) {
			return true
		}
	}
	return false
}

func sortColumnFromRune(r rune) (int, bool) {
	shiftDigits := map[rune]int{'!': 0, '@': 1, '#': 2, '$': 3, '%': 4, '^': 5, '&': 6, '*': 7, '(': 8}
	col, ok := shiftDigits[r]
	return col, ok
}

func clusterScoped(resource string) bool {
	switch resource {
	case "namespaces", "nodes", "persistentvolumes", "storageclasses", "volumeattachments", "clusterroles", "clusterrolebindings", "apiservices":
		return true
	default:
		return false
	}
}

func pluginPaths(paths []string) []string {
	if len(paths) > 0 {
		return paths
	}
	return []string{
		filepath.Join(config.DefaultK9sDir(), "plugins.yaml"),
		filepath.Join(config.DefaultDir(), "plugins.yaml"),
	}
}
