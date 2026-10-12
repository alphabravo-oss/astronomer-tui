//revive:disable:exported package-comments
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/alphabravo-oss/astrotui/internal/session"
	uitheme "github.com/alphabravo-oss/astrotui/internal/ui/theme"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

type Options struct {
	ReadOnly       bool
	StartupCommand string
	ConfigDir      string
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
	baseConfig          config.Config
	marked              map[types.UID]*unstructured.Unstructured
	viewCancel          context.CancelFunc
	customCancel        context.CancelFunc
	viewAlias           string
	viewAliasGVR        schema.GroupVersionResource
	screen              tcell.Screen
	discoveryGeneration uint64
	renderCache         map[*unstructured.Unstructured][]views.Cell
	aiCancel            context.CancelFunc
	custom              config.Custom
	customError         error
	logRows             []logs.Entry
	history             []navigationState
	relatedPredicate    func(*unstructured.Unstructured) bool
	metricsError        error
	catalog             *cluster.Catalog
	namespaceCatalog    []string
	discoveryError      error
	operationCancel     context.CancelFunc
	operationID         uint64
	cacheScope          string
	renderers           map[schema.GroupVersionResource]views.Renderer
	updates             chan uiUpdate
	updatePending       atomic.Bool
	options             Options
	app                 *tview.Application
	table               *tview.Table
	cache               *cluster.ResourceCache
	currentGVR          schema.GroupVersionResource
	objects             []*unstructured.Unstructured
	visibleObjects      []*unstructured.Unstructured
	filter              string
	sortCol             int
	sortDesc            bool
	mu                  sync.RWMutex
	resourceCancel      context.CancelFunc
	cacheCancel         context.CancelFunc
	ctx                 context.Context
	generation          uint64
	loading             bool
	watchError          error
	wide                bool
	root                *tview.Flex
	header              *tview.TextView
	status              *tview.TextView
	hints               *tview.TextView
	logCancel           context.CancelFunc
	crashes             CrashReporter
	renderer            views.Renderer
	pluginFile          plugins.File
	skin                uitheme.Skin
}

func New(options Options) *App {
	if options.ReadOnly {
		options.Config.ReadOnly = true
	}
	pluginFile, _ := plugins.LoadMerged(pluginPaths(options.PluginPaths)...)
	app := &App{
		baseConfig: options.Config, options: options,
		app:        tview.NewApplication(),
		table:      tview.NewTable().SetSelectable(true, false),
		crashes:    CrashReporter{Dir: DefaultCrashDir()},
		pluginFile: pluginFile,
		skin:       resolveSkin(options),
	}
	contextName, clusterName := app.contextNames()
	app.custom, app.customError = config.LoadScopedCustom(app.customDir(), clusterName, contextName)
	if cfg, err := config.ApplyContext(app.customDir(), clusterName, contextName, app.baseConfig); err == nil {
		app.options.Config = cfg
	} else {
		app.customError = err
	}
	if app.options.StartupCommand == "" {
		app.options.StartupCommand = app.options.Config.Contexts[contextName].View
	}
	app.skin = resolveSkin(app.options)
	app.initLayout()
	app.applySkin()
	return app
}

func (a *App) Run(ctx context.Context) error {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor && a.screen == nil {
		screen, err := tcell.NewScreen()
		if err != nil {
			return err
		}
		a.SetScreen(screen)
	}

	ctx, cancel := context.WithCancel(ctx)
	a.ctx = ctx
	defer cancel()
	defer a.stopBackground()
	gvr := initialResourceGVR(a.options.InitialState)
	a.cacheScope = namespaceFor(a.options.Client, gvr)
	a.cache = cluster.NewScopedResourceCache(a.options.Client.Dynamic, 30*time.Minute, a.cacheScope)
	cacheCtx, cacheCancel := context.WithCancel(ctx)
	a.cacheCancel = cacheCancel
	a.cache.Start(cacheCtx)
	a.switchResource(ctx, gvr)
	if a.options.InitialState.LastFilter != "" {
		a.mu.Lock()
		a.filter = a.options.InitialState.LastFilter
		a.mu.Unlock()
	}
	a.refreshDiscovery()
	a.watchCustom(ctx)
	a.watchSkinUpdates(ctx)

	a.bindTableKeys(ctx)
	a.showMain()
	if a.options.StartupCommand != "" {
		a.executeConfiguredCommand(a.options.StartupCommand)
	}
	go func() { <-ctx.Done(); a.app.Stop() }()
	err := a.app.Run()
	if a.options.SessionStore != nil {
		if saveErr := a.options.SessionStore.Save(a.sessionState()); saveErr != nil {
			err = errors.Join(err, fmt.Errorf("save session: %w", saveErr))
		}
	}
	return err
}

func (a *App) bindTableKeys(ctx context.Context) {
	a.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			a.toggleMarked()
			return nil
		}
		if event.Key() == tcell.KeyCtrlK {
			a.deleteMarked(true)
			return nil
		}
		if event.Key() == tcell.KeyCtrlA {
			a.showAliasList()
			return nil
		}
		if event.Key() == tcell.KeyCtrlS {
			a.saveSelectedResource()
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'R' {
			a.showRelationships()
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'b' {
			a.goBack()
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'E' {
			if o := a.selectedObject(); o != nil {
				a.showObjectEvents(o.DeepCopy())
			}
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == '?' {
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
			a.safe(a.openSelected)
			return nil
		}
		if event.Key() == tcell.KeyRune && (event.Rune() == 'd' || event.Rune() == 'y') {
			if event.Rune() == 'd' {
				a.safe(a.showDescribe)
				return nil
			}
			a.safe(a.showDetail)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == '/' {
			a.safe(a.showFilter)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == ':' {
			a.safe(func() { a.showCommand(ctx) })
			return nil
		}
		if event.Key() == tcell.KeyCtrlD {
			if len(a.marked) > 0 {
				a.deleteMarked(false)
				return nil
			}
			a.safe(a.showDelete)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'e' {
			a.safe(a.showEdit)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'l' {
			a.safe(a.showLogsPlan)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'x' {
			if obj := a.selectedObject(); obj != nil && obj.GetKind() == "Secret" {
				a.showDecodedSecret()
				return nil
			}
			if obj := a.selectedObject(); obj != nil && obj.GetKind() == "Node" {
				a.showNodeShell()
				return nil
			}
			a.safe(a.showExecPlan)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'f' {
			a.safe(a.showPortForwardPlan)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 's' {
			a.safe(a.showScale)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'r' {
			a.safe(a.showRestart)
			return nil
		}
		if col, ok := sortColumnFromRune(event.Rune()); ok && event.Key() == tcell.KeyRune {
			if a.renderer != nil {
				columns := a.displayColumns(a.renderer)
				if col < len(columns) {
					a.setSort(columns[col])
				}
			}
			return nil
		}
		switch {
		case event.Key() == tcell.KeyEsc:
			a.filter = ""
			a.renderCurrent()
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == '0':
			a.switchNamespace("all")
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'w':
			a.wide = !a.wide
			a.renderCurrent()
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'q':
			a.app.Stop()
			return nil
		}
		if a.customKey(event) {
			return nil
		}
		return event
	})
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
		if state.Namespace == "" {
			state.Namespace = "all"
		}
	}
	a.mu.RLock()
	state.LastView = a.currentGVR.Resource
	if _, builtin := views.Builtins()[a.currentGVR]; !builtin && a.currentGVR.Resource != "" {
		state.LastView = a.currentGVR.Group + "/" + a.currentGVR.Version + "/" + a.currentGVR.Resource
	}
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
	for col, index := range a.displayColumns(renderer) {
		header := renderer.Header()[index]
		name := header.Name
		if index == sortCol {
			if sortDesc {
				name += " ▼"
			} else {
				name += " ▲"
			}
		}
		a.table.SetCell(0, col, tview.NewTableCell(name).SetExpansion(0).SetTextColor(skinColor(a.skin.Accent, tcell.ColorAqua)).SetSelectable(false))
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
				a.queueUpdate(ctx, func() {
					a.applySkinUpdate(update.Skin)
				})
			}
		}
	}()
}

func (a *App) applySkinUpdate(skin uitheme.Skin) {
	a.options.Skin = skin
	a.skin = resolveSkin(a.options)
	a.applySkin()
	a.renderCurrent()
}

// Resource callbacks only invalidate. All widget and model commits happen together
// on the tview event loop, and a generation rejects obsolete queued work.
func (a *App) applyResourceRefresh(cache *cluster.ResourceCache, generation uint64) {
	if a.cache != cache || a.generation != generation {
		return
	}
	wasLoading := a.loading
	a.loading = !cache.Ensure(a.currentGVR).HasSynced()
	if wasLoading && !a.loading {
		a.mu.Lock()
		a.visibleObjects = nil
		a.mu.Unlock()
		a.table.Select(0, 0)
	}
	a.watchError = cache.LastError(a.currentGVR)
	a.reloadCurrent()
}

func (a *App) updateTitle() { a.updateChrome() }

func (a *App) switchResource(ctx context.Context, gvr schema.GroupVersionResource) bool {
	renderer := rendererForGVR(gvr)
	if custom := a.renderers[gvr]; custom != nil {
		renderer = custom
	}
	a.ensureScope(gvr)
	if renderer == nil || a.cache == nil {
		a.showMessage("Unknown resource", gvr.String())
		return false
	}
	if a.resourceCancel != nil {
		a.resourceCancel()
	}
	watchCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.resourceCancel = cancel
	a.generation++
	generation := a.generation
	a.currentGVR = gvr
	a.marked = nil
	a.relatedPredicate = nil
	a.renderer = a.configuredRenderer(renderer)
	a.objects = nil
	a.visibleObjects = nil
	a.filter = ""
	a.sortCol = 0
	for i, column := range a.renderer.Header() {
		if column.Name == "NAME" {
			a.sortCol = i
			break
		}
	}
	a.sortDesc = false
	if v, ok := a.currentCustomView(gvr); ok {
		column, direction, _ := strings.Cut(v.SortColumn, ":")
		for i, c := range a.renderer.Header() {
			if c.Name == strings.ToUpper(column) {
				a.sortCol = i
				a.sortDesc = direction == "desc"
				break
			}
		}
	}
	cache := a.cache
	a.mu.Unlock()
	a.loading = !cache.Ensure(gvr).HasSynced()
	a.watchError = cache.LastError(gvr)
	a.metricsError = nil
	a.watchMetrics(watchCtx)
	a.watchServerTable(watchCtx)
	a.reloadCurrent()
	events, unsubscribe := cache.SubscribeWithCancel(gvr)
	go func() {
		defer unsubscribe()
		// A periodic sync check handles an empty initial list (no add event).
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		dirty, synced := true, false
		lastRefresh := time.Now()
		var pending atomic.Bool
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-events:
				dirty = true
			case <-ticker.C:
				nowSynced := cache.Ensure(gvr).HasSynced()
				if time.Since(lastRefresh) > 30*time.Second {
					dirty = true
				}
				if !dirty && synced == nowSynced {
					continue
				}
				if !pending.CompareAndSwap(false, true) {
					continue
				}
				dirty, synced = false, nowSynced
				lastRefresh = time.Now()
				a.queueUpdate(watchCtx, func() {
					pending.Store(false)
					a.applyResourceRefresh(cache, generation)
				})
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
	view := tview.NewTextView().SetText(HelpText(a.actionCatalog()) + parityHelp()).SetScrollable(true)
	view.SetTitle(" Help · Esc to return ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyRune && event.Rune() == 'q' {
			a.showMain()
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) showFilter() {
	original := a.filter
	filterCtx, cancel := context.WithCancel(a.backgroundContext())
	var timer *time.Timer
	sequence := uint64(0)
	input := tview.NewInputField().SetLabel("/ ").SetText(original).SetFieldWidth(0)
	feedback := tview.NewTextView().SetText("regex | !inverse | -l app=api | -f fuzzy | -t literal · Enter apply · Esc cancel")
	input.SetChangedFunc(func(text string) {
		if _, err := compileResourceFilter(text); err != nil {
			feedback.SetText("Invalid filter: " + err.Error())
			return
		}
		a.mu.Lock()
		a.filter = text
		a.mu.Unlock()
		sequence++
		current := sequence
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(40*time.Millisecond, func() {
			a.queueUpdate(filterCtx, func() {
				if sequence == current {
					a.renderCurrent()
				}
			})
		})
		feedback.SetText("Enter apply · Esc restore previous filter")
	})
	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			if _, err := compileResourceFilter(input.GetText()); err != nil {
				return
			}
			a.renderCurrent()
		case tcell.KeyEsc:
			a.mu.Lock()
			a.filter = original
			a.mu.Unlock()
			a.renderCurrent()
		default:
			return
		}
		cancel()
		if timer != nil {
			timer.Stop()
		}
		a.showMain()
	})
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.root, 0, 1, false).AddItem(feedback, 1, 0, false).AddItem(input, 1, 0, true)
	a.app.SetRoot(layout, true).SetFocus(input)
}

func (a *App) showCommand(ctx context.Context) {
	input := tview.NewInputField().
		SetLabel(":").
		SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if a.specialCommand(input.GetText()) {
				return
			}
			command, ok := parseCommand(input.GetText())
			if !ok {
				a.showMessage("Unknown command", ":"+input.GetText())
				return
			}
			switch command.Kind {
			case "access":
				a.showAccessReview()
				return
			case "diagnose":
				a.showStorageDiagnostics()
				return
			case "refresh":
				a.refreshDiscovery()
			case "resource":
				a.executeConfiguredCommand(command.Value)
				return
			case "namespace":
				if command.Value == "" {
					a.showNamespacePicker()
					return
				}
				a.switchNamespace(command.Value)
			case "context":
				if command.Value == "" {
					a.showContextPicker(ctx)
					return
				}
				a.switchContext(ctx, command.Value)
				return
			case "quit":
				a.app.Stop()
				return
			case "port-forward-manager":
				a.showPortForwardManager()
				return
			}
		}
		a.showMain()
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
	if namespace == "all" || namespace == "*" {
		namespace = ""
	}
	a.options.Client.Namespace = namespace
	if a.cacheCancel != nil {
		a.switchResource(a.backgroundContext(), a.currentGVR)
	} else {
		a.reloadCurrent()
	}
}

func (a *App) switchContext(ctx context.Context, contextName string) bool {
	return a.switchContextAfter(ctx, contextName, nil)
}
func (a *App) switchContextAfter(ctx context.Context, contextName string, after func()) bool {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return false
	}
	namespace := ""
	if a.options.Client != nil {
		namespace = a.options.Client.Namespace
	}
	factory := a.clientFactory()
	a.operation("Switch context", func(workCtx context.Context) (func(), error) {
		client, err := factory(workCtx, cluster.ClientOptions{Context: contextName, Namespace: namespace})
		if err != nil {
			return nil, err
		}
		if namespace == "" {
			client.Namespace = ""
		}
		return func() {
			if a.cacheCancel != nil {
				a.cacheCancel()
			}
			if a.resourceCancel != nil {
				a.resourceCancel()
			}
			if a.options.PortForwards != nil {
				for _, forward := range a.options.PortForwards.List() {
					a.options.PortForwards.Kill(forward.ID)
				}
			}
			a.options.Client = client
			contextName, clusterName := a.contextNames()
			if cfg, err := config.ApplyContext(a.customDir(), clusterName, contextName, a.baseConfig); err == nil {
				a.options.Config = cfg
				if ns := cfg.Contexts[contextName].Namespace; ns != "" {
					client.Namespace = ns
					if ns == "all" || ns == "*" {
						client.Namespace = ""
					}
				}
			} else {
				a.options.Config = a.baseConfig
				a.customError = err
			}
			a.applySkinUpdate(a.options.Skin)
			a.custom = config.Custom{}
			a.viewAlias = ""
			a.watchCustom(a.backgroundContext())
			a.catalog = nil
			a.namespaceCatalog = nil
			a.renderers = nil
			a.cacheScope = namespaceFor(client, a.currentGVR)
			a.cache = cluster.NewScopedResourceCache(client.Dynamic, 30*time.Minute, a.cacheScope)
			cacheCtx, cancel := context.WithCancel(a.backgroundContext())
			a.cacheCancel = cancel
			a.cache.Start(cacheCtx)
			gvr := a.currentGVR
			if gvr.Resource == "" {
				gvr = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
			}
			a.switchResource(a.backgroundContext(), gvr)
			a.refreshDiscovery()
			a.showMain()
			if after != nil {
				after()
			}
		}, nil
	})
	return true
}

func (a *App) clientFactory() func(context.Context, cluster.ClientOptions) (*cluster.Client, error) {
	if a.options.ClientFactory != nil {
		return a.options.ClientFactory
	}
	return cluster.NewClient
}

func resolveSkin(options Options) uitheme.Skin {
	if options.Client != nil {
		if c, ok := options.Config.Contexts[options.Client.Context]; ok && c.Theme != "" {
			if skin, ok := uitheme.Defaults()[c.Theme]; ok {
				return skin
			}
		}
	}
	if options.Skin.Name != "" {
		return options.Skin
	}
	name := options.Config.Theme
	if options.Client != nil {
		if c, ok := options.Config.Contexts[options.Client.Context]; ok && c.Theme != "" {
			name = c.Theme
		}
	}
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
	if err := safety.Authorize(a.options.Config, a.options.Client.Context, safety.Delete, ""); err != nil {
		a.showMessage("Delete unavailable", err.Error())
		return
	}
	modal := tview.NewModal().SetText(fmt.Sprintf("Delete %s/%s in %s on %s?", req.Kind, req.ResourceName, req.Namespace, a.options.Client.Context)).
		AddButtons([]string{"Cancel", "Delete"}).SetDoneFunc(func(index int, _ string) {
		if index == 1 {
			a.confirmDelete(req, "")
			return
		}
		a.showMain()
	})
	a.app.SetRoot(modal, false)
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
			a.showMain()
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

func (a *App) selectedPod() (corev1.Pod, bool) {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Pod" {
		return corev1.Pod{}, false
	}
	return toTyped[corev1.Pod](obj)
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
		a.showMain()
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
		a.showMain()
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
	return kind == "Deployment" || kind == "StatefulSet" || kind == "ReplicaSet"
}

func restartableKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet"
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
		a.showMain()
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
	timeout := 30 * time.Second
	if req.Name == "drain" {
		timeout = 10 * time.Minute
	}
	a.operationWithTimeout(successTitle, timeout, func(ctx context.Context) (func(), error) {
		result, err := executor.Run(ctx, req)
		if err != nil {
			return nil, err
		}
		return func() { a.showMessage(successTitle, result.Message) }, nil
	})
}

func (a *App) showMessage(title, body string) {
	modal := tview.NewModal().
		SetText(tview.Escape(body)).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) { a.showMain() })
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

func (a *App) reloadCurrent() {
	a.renderCache = nil
	if a.cache != nil && a.renderer != nil {
		items, err := a.cache.List(a.currentGVR, a.resourceNamespace(a.currentGVR), labels.Everything())
		if err != nil {
			a.watchError = err
		} else {
			a.mu.Lock()
			a.objects = items
			a.mu.Unlock()
		}
	}
	a.renderCurrent()
}

func (a *App) renderCurrent() {
	if a.renderer == nil {
		a.updateTitle()
		return
	}
	selected := a.selectedObject()
	oldRow, _ := a.table.GetSelection()
	a.mu.RLock()
	objects := append([]*unstructured.Unstructured(nil), a.objects...)
	filter, sortCol, sortDesc := a.filter, a.sortCol, a.sortDesc
	a.mu.RUnlock()
	if a.relatedPredicate != nil {
		filtered := objects[:0]
		for _, obj := range objects {
			if a.relatedPredicate(obj) {
				filtered = append(filtered, obj)
			}
		}
		objects = filtered
	}
	if a.renderCache == nil {
		a.renderCache = map[*unstructured.Unstructured][]views.Cell{}
	}
	rows, visible := buildRows(cachedRenderer{Renderer: a.renderer, rows: a.renderCache}, objects, filter, sortCol, sortDesc)
	a.table.Clear()
	a.drawHeader(a.renderer)
	columns := a.displayColumns(a.renderer)
	headers := a.renderer.Header()
	for row, cells := range rows {
		for col, index := range columns {
			if index >= len(cells) {
				continue
			}
			cell := tview.NewTableCell(tview.Escape(cells[index].Text)).SetTextColor(skinColor(a.skin.FG, tcell.ColorWhite)).SetMaxWidth(48)
			if headers[index].AlignRight {
				cell.SetAlign(tview.AlignRight)
			}
			if headers[index].Name == "STATUS" {
				cell.SetTextColor(a.stateColor(cells[index].Text))
			}
			if _, marked := a.marked[visible[row].GetUID()]; marked {
				cell.SetAttributes(tcell.AttrBold)
				if col == 0 {
					cell.SetText("* " + tview.Escape(cells[index].Text))
				}
			}
			a.table.SetCell(row+1, col, cell)
		}
	}
	a.mu.Lock()
	a.visibleObjects = visible
	a.mu.Unlock()
	row := min(max(oldRow, 1), len(visible))
	for i, obj := range visible {
		if sameObject(selected, obj) {
			row = i + 1
			break
		}
	}
	a.table.Select(row, 0)
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
		SetText(tview.Escape(detail.Body))
	view.SetTitle(" " + detail.Title + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyRune && event.Rune() == 'q' {
			a.showMain()
			return nil
		}
		if event.Key() == tcell.KeyCtrlE || event.Key() == tcell.KeyRune && event.Rune() == 'a' {
			a.showManifestAI(view, detail.Body)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) namespaceNames() []string {
	names := map[string]struct{}{}
	if a.options.Client != nil && a.options.Client.Namespace != "" {
		names[a.options.Client.Namespace] = struct{}{}
	}
	for _, name := range a.namespaceCatalog {
		names[name] = struct{}{}
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
	lines = append(lines, "/  filter - Regex; !inverse; -l labels; -f fuzzy; -t literal")
	lines = append(lines, "Esc  clear filter / return", "j/k or arrows  move; PgUp/PgDn  page; Home/End  jump", "0  all namespaces; :ns  namespace picker; :ctx  context picker", "w  compact/wide columns; d / enter / y  details; Ctrl+D  confirm delete", "q / :q / Ctrl+C  quit")
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
		UID:          obj.GetUID(),
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
	match, err := compileResourceFilter(filter)
	if err != nil {
		return nil, nil
	}
	for _, obj := range objects {
		row := renderer.Row(obj)
		if !match(row, obj) {
			continue
		}
		pairs = append(pairs, rowObject{row: row, obj: obj})
	}
	headers := renderer.Header()
	sort.Slice(pairs, func(i, j int) bool {
		cmp := 0
		if sortCol >= 0 && sortCol < len(pairs[i].row) && sortCol < len(pairs[j].row) {
			column := views.Column{}
			if sortCol < len(headers) {
				column = headers[sortCol]
			}
			cmp = compareColumnCells(column, pairs[i].row[sortCol].Text, pairs[j].row[sortCol].Text, pairs[i].obj, pairs[j].obj)
		}
		if cmp == 0 {
			l, r := pairs[i].obj, pairs[j].obj
			cmp = strings.Compare(l.GetNamespace()+"/"+l.GetName()+"/"+string(l.GetUID()), r.GetNamespace()+"/"+r.GetName()+"/"+string(r.GetUID()))
		}
		if sortDesc {
			return cmp > 0
		}
		return cmp < 0
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
		filepath.Join(config.DefaultK9sDir(), "plugins"),
		filepath.Join(config.DefaultDir(), "plugins"),
	}
}

// SetScreen supplies a terminal or simulation before Run and applies terminal policy.
func (a *App) SetScreen(screen tcell.Screen) *App {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		screen = monochromeScreen{Screen: screen, background: skinColor(a.skin.BG, tcell.ColorBlack)}
	}
	a.screen = screen
	a.app.SetScreen(screen)
	return a
}
