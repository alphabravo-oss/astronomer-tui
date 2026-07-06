//revive:disable:exported package-comments
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
	MetricsPoller  metricsSnapshotter
}

type App struct {
	options          Options
	app              *tview.Application
	table            *tview.Table
	nav              *Nav
	pages            *tview.Pages
	header           *tview.TextView
	crumbsView       *tview.TextView
	flashView        *tview.TextView
	cache            *cluster.ResourceCache
	currentGVR       schema.GroupVersionResource
	objects          []*unstructured.Unstructured
	visibleObjects   []*unstructured.Unstructured
	filter           string
	sortCol          int
	sortDesc         bool
	mu               sync.RWMutex
	resourceCancel   context.CancelFunc
	crashes          CrashReporter
	renderer         views.Renderer
	pluginFile       plugins.File
	skin             uitheme.Skin
	clusterScopedDyn map[schema.GroupVersionResource]bool
	crdRenderers     map[schema.GroupVersionResource]views.Renderer
	metrics          metricsSnapshotter
	flashClear       *time.Timer
	runCtx           context.Context //nolint:containedctx // retained so registry key actions can reach the run context.
	tableKeymap      *Keymap
	lastSelKey       string
	labelSelector    labels.Selector
	drillNodeName    string
	tableStack       []tableState
	marks            map[string]struct{}
	lastMarkRow      int
}

// markCount returns the number of marked rows.
func (a *App) markCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.marks)
}

// tableState captures the root-table view so drill-down can restore it on back.
type tableState struct {
	gvr      schema.GroupVersionResource
	filter   string
	sortCol  int
	sortDesc bool
	selector labels.Selector
	nodeName string
	selKey   string
	crumb    string
}

// drillScope carries the extra scoping a drilled view applies on top of a GVR.
type drillScope struct {
	selector labels.Selector
	nodeName string
	crumb    string
}

func New(options Options) *App {
	pluginFile, _ := plugins.LoadMerged(pluginPaths(options.PluginPaths)...)
	tapp := tview.NewApplication()
	pages := tview.NewPages()
	app := &App{
		options:    options,
		app:        tapp,
		table:      tview.NewTable().SetSelectable(true, false),
		pages:      pages,
		header:     tview.NewTextView().SetDynamicColors(true),
		crumbsView: tview.NewTextView().SetDynamicColors(true),
		flashView:  tview.NewTextView().SetDynamicColors(true),
		nav:        newNav(tapp, pages),
		crashes:    CrashReporter{Dir: DefaultCrashDir()},
		pluginFile: pluginFile,
		skin:       resolveSkin(options),
	}
	app.table.SetSelectionChangedFunc(func(row, _ int) { app.rememberSelection(row) })
	app.applySkin()
	return app
}

// rootLayout composes the persistent chrome (header, crumbs, flash) around the
// navigation pages.
func (a *App) rootLayout() tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.crumbsView, 1, 0, false).
		AddItem(a.flashView, 1, 0, false)
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

	a.runCtx = ctx
	a.table.SetTitle(fmt.Sprintf(" astrotui [%s/%s] %s ", a.options.Client.Context, a.options.Client.Namespace, gvr.Resource)).SetBorder(true)
	a.tableKeymap = a.buildTableKeymap()
	a.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if a.tableKeymap.handle(event) {
			return nil
		}
		if col, ok := sortColumnFromRune(event.Rune()); ok {
			a.setSort(col)
			return nil
		}
		return event
	})
	a.nav.SetRootView(a.table, "")
	a.app.SetRoot(a.rootLayout(), true)
	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			a.safe(a.handleEsc)
			return nil
		}
		return event
	})
	a.updateChrome()
	if a.options.MetricsPoller != nil {
		a.startMetrics(ctx, a.options.MetricsPoller)
	}
	a.showSplash(ctx)
	err := a.app.Run()
	if a.options.SessionStore != nil {
		_ = a.options.SessionStore.Save(a.sessionState())
	}
	return err
}

// buildTableKeymap registers the resource-table bindings in the registry so the
// help overlay (and later hotkeys.yaml) can introspect them.
func (a *App) buildTableKeymap() *Keymap {
	k := &Keymap{}
	wrap := func(fn func()) func() { return func() { a.safe(fn) } }
	k.Add(keyBinding(tcell.KeyEnter, "enter", "Drill down (or describe)", "table", true, wrap(a.drillDown)))
	k.Add(runeBinding('d', "describe", "Describe resource with events", "table", true, wrap(a.showDescribe)))
	k.Add(runeBinding('y', "yaml", "View resource YAML", "table", true, wrap(a.showDetail)))
	k.Add(runeBinding('e', "edit", "Edit YAML in $EDITOR", "table", true, wrap(a.showEdit)))
	k.Add(keyBinding(tcell.KeyCtrlD, "delete", "Delete selected resource", "table", true, wrap(a.showDelete)))
	k.Add(runeBinding('s', "scale", "Scale replicas", "table", true, wrap(a.showScale)))
	k.Add(runeBinding('r', "restart", "Rollout restart", "table", true, wrap(a.showRestart)))
	k.Add(runeBinding('l', "logs", "Open logs", "table", true, wrap(a.showLogsPlan)))
	k.Add(runeBinding('x', "exec", "Exec into pod", "table", true, wrap(a.showExecPlan)))
	k.Add(runeBinding('f', "port-forward", "Forward a port", "table", true, wrap(a.showPortForwardPlan)))
	k.Add(runeBinding('/', "filter", "Filter visible rows", "table", true, wrap(a.showFilter)))
	k.Add(runeBinding(':', "command", "Command prompt", "table", true, wrap(func() { a.showCommand(a.runCtx) })))
	k.Add(runeBinding('0', "all-namespaces", "Show all namespaces", "table", true, wrap(func() { a.switchNamespace("all") })))
	k.Add(keyBinding(tcell.KeyCtrlP, "palette", "Command palette", "global", true, wrap(a.showPalette)))
	k.Add(keyBinding(tcell.KeyCtrlE, "ai-explain", "AI explain selection", "global", true, wrap(a.showFailureAI)))
	k.Add(runeBinding('?', "help", "Show this overlay", "global", true, wrap(a.showHelp)))
	// Semantic sort keys (Shift+letter) resolve to a column by header name.
	for _, sk := range semanticSortKeys() {
		col := sk.column
		k.Add(runeBinding(sk.rune, "sort:"+strings.ToLower(col), "Sort by "+col, "table", false, func() { a.sortByColumnName(col) }))
	}
	return k
}

type semanticSortKey struct {
	rune   rune
	column string
}

// semanticSortKeys maps Shift+<letter> to a target column header (k9s parity).
func semanticSortKeys() []semanticSortKey {
	return []semanticSortKey{
		{'N', "NAME"},
		{'A', "AGE"},
		{'S', "STATUS"},
		{'R', "READY"},
		{'C', "CPU"},
		{'M', "MEM"},
		{'P', "PODS"},
	}
}

// sortByColumnName sets the sort to the first column whose header matches name.
func (a *App) sortByColumnName(name string) {
	if a.renderer == nil {
		return
	}
	for i, header := range a.renderer.Header() {
		if strings.EqualFold(header.Name, name) {
			a.setSort(i)
			return
		}
	}
	a.Flash(FlashInfo, "no %s column in this view", name)
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
	namespace := a.namespaceFor(client, gvr)
	a.mu.RLock()
	selector := a.labelSelector
	nodeName := a.drillNodeName
	a.mu.RUnlock()
	if selector == nil {
		selector = labels.Everything()
	}
	items, err := cache.List(gvr, namespace, selector)
	if err != nil {
		a.Flash(FlashError, "%s: list failed: %s", gvr.Resource, err.Error())
		return
	}
	if nodeName != "" {
		items = filterByNodeName(items, nodeName)
	}
	a.mu.RLock()
	filter, sortCol, sortDesc := a.filter, a.sortCol, a.sortDesc
	a.mu.RUnlock()
	rows, visible := buildRows(renderer, items, filter, sortCol, sortDesc, a.rowContext(gvr, items))
	a.mu.Lock()
	a.objects = items
	a.visibleObjects = visible
	a.mu.Unlock()
	a.app.QueueUpdateDraw(func() {
		a.paintRows(renderer, rows, visible)
		a.reselectByKey(a.selKey(), visible)
		a.updateTitle()
	})
}

// paintRows clears and repopulates the table body from pre-rendered rows,
// applying per-row state coloring where a cell has no explicit color.
func (a *App) paintRows(renderer views.Renderer, rows [][]views.Cell, visible []*unstructured.Unstructured) {
	a.table.Clear()
	a.drawHeader(renderer)
	gvr := renderer.GVR()
	for row, cells := range rows {
		rowColor := ""
		if row < len(visible) {
			rowColor = views.StateColor(gvr, visible[row])
		}
		for col, cell := range cells {
			if cell.Color == "" {
				cell.Color = rowColor
			}
			a.setTableCell(row+1, col, cell)
		}
	}
}

func objectKey(obj *unstructured.Unstructured) string {
	if obj == nil {
		return ""
	}
	return obj.GetNamespace() + "/" + obj.GetName()
}

// selKey returns the remembered identity of the selected row.
func (a *App) selKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastSelKey
}

// rememberSelection records the identity of the row at the given table row so
// selection survives watch-driven table rebuilds.
func (a *App) rememberSelection(row int) {
	obj := selectedObject(a.visibleObjectsSnapshot(), row)
	a.mu.Lock()
	a.lastSelKey = objectKey(obj)
	a.mu.Unlock()
}

// reselectByKey restores the cursor to the row whose object matches key.
func (a *App) reselectByKey(key string, visible []*unstructured.Unstructured) {
	if key == "" {
		return
	}
	for i, obj := range visible {
		if objectKey(obj) == key {
			a.table.Select(i+1, 0)
			return
		}
	}
}

func (a *App) updateTitle() {
	a.mu.RLock()
	resource := a.currentGVR.Resource
	a.mu.RUnlock()
	if resource == "" {
		resource = "resources"
	}
	title := fmt.Sprintf(" astrotui [%s/%s] %s ", a.options.Client.Context, displayNamespace(a.options.Client.Namespace), resource)
	a.mu.RLock()
	filter := a.filter
	matched := len(a.visibleObjects)
	total := len(a.objects)
	a.mu.RUnlock()
	if filter != "" {
		desc := ParseRowFilter(filter).Describe()
		title += fmt.Sprintf(" filter=%s (%d/%d) ", desc, matched, total)
	}
	if marks := a.markCount(); marks > 0 {
		title += fmt.Sprintf(" [%d marked] ", marks)
	}
	a.table.SetTitle(title)
}

func displayNamespace(namespace string) string {
	if namespace == "" {
		return "all"
	}
	return namespace
}

func (a *App) switchResource(ctx context.Context, gvr schema.GroupVersionResource) bool {
	return a.switchResourceScoped(ctx, gvr, drillScope{})
}

// switchResourceScoped switches the root table to a GVR, applying an optional
// drill scope (label selector or node-name field filter) atomically so the
// first refresh already honors it.
func (a *App) switchResourceScoped(ctx context.Context, gvr schema.GroupVersionResource, scope drillScope) bool {
	renderer := a.rendererFor(ctx, gvr)
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
	a.labelSelector = scope.selector
	a.drillNodeName = scope.nodeName
	cache := a.cache
	client := a.options.Client
	a.mu.Unlock()
	if scope.crumb != "" {
		a.nav.SetTopCrumb(scope.crumb)
	} else {
		a.nav.SetTopCrumb(gvr.Resource)
	}
	cache.Ensure(gvr)
	a.drawHeader(renderer)
	events := cache.Subscribe(gvr)
	errs := cache.SubscribeErrors(gvr)
	go func() {
		cache.WaitForSync(watchCtx, gvr)
		a.refresh(cache, client, gvr, renderer)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-events:
				a.refresh(cache, client, gvr, renderer)
			case err := <-errs:
				if err != nil {
					a.Flash(FlashError, "%s: %s", gvr.Resource, watchErrorHint(err))
				}
			}
		}
	}()
	return true
}

// watchErrorHint condenses a watch/list error into a short, actionable message.
func watchErrorHint(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "Forbidden"):
		return "watch forbidden (check RBAC)"
	case strings.Contains(msg, "the server could not find") || strings.Contains(msg, "could not find the requested resource"):
		return "resource not served by the API"
	default:
		return "watch failed: " + msg
	}
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

// rendererFor prefers builtin renderers, then CRD printer columns discovered
// from the cluster, then the generic NAME/NAMESPACE/AGE fallback.
func (a *App) rendererFor(ctx context.Context, gvr schema.GroupVersionResource) views.Renderer {
	if gvr.Resource == "" || gvr.Version == "" {
		return nil
	}
	if renderer, ok := views.Builtins()[gvr]; ok {
		return renderer
	}
	a.mu.RLock()
	cached, ok := a.crdRenderers[gvr]
	a.mu.RUnlock()
	if ok {
		return cached
	}
	renderer := a.lookupCRDRenderer(ctx, gvr)
	if renderer == nil {
		renderer = views.GenericRenderer{Resource: gvr}
	}
	a.mu.Lock()
	if a.crdRenderers == nil {
		a.crdRenderers = map[schema.GroupVersionResource]views.Renderer{}
	}
	a.crdRenderers[gvr] = renderer
	a.mu.Unlock()
	return renderer
}

func (a *App) lookupCRDRenderer(ctx context.Context, gvr schema.GroupVersionResource) views.Renderer {
	if gvr.Group == "" || a.options.Client == nil || a.options.Client.Dynamic == nil {
		return nil
	}
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	obj, err := a.options.Client.Dynamic.Resource(crdGVR).Get(lookupCtx, gvr.Resource+"."+gvr.Group, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd); err != nil {
		return nil
	}
	if crd.Spec.Scope == apiextensionsv1.ClusterScoped {
		a.mu.Lock()
		if a.clusterScopedDyn == nil {
			a.clusterScopedDyn = map[schema.GroupVersionResource]bool{}
		}
		a.clusterScopedDyn[gvr] = true
		a.mu.Unlock()
	}
	return views.RendererForCRD(&crd, gvr.Version)
}

func (a *App) showHelp() {
	modal := tview.NewModal().
		SetText(a.helpOverlayText()).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) { a.closeModal() })
	a.showModal(modal)
}

// helpOverlayText builds the help overlay from the live keymap registry and
// command catalog, so it never drifts from the actual bindings.
func (a *App) helpOverlayText() string {
	lines := []string{"Keys"}
	if a.tableKeymap != nil {
		for _, row := range a.tableKeymap.HelpRows() {
			lines = append(lines, fmt.Sprintf("  %-8s %s", row[0], row[1]))
		}
	}
	lines = append(lines, "", "Commands")
	for _, spec := range commandCatalog() {
		lines = append(lines, fmt.Sprintf("  %-14s %s", spec.usage, spec.help))
	}
	if len(a.pluginFile.Plugins) > 0 {
		lines = append(lines, "", "Plugins")
		for name, plugin := range a.pluginFile.Plugins {
			key := plugin.ShortCut
			if key == "" {
				key = name
			}
			lines = append(lines, fmt.Sprintf("  %-8s %s", key, plugin.Description))
		}
	}
	return strings.Join(lines, "\n")
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
			expr := input.GetText()
			if rf := ParseRowFilter(expr); rf.Err() != nil {
				a.Flash(FlashError, "filter: %s", rf.Err().Error())
				a.closeModal()
				return
			}
			a.mu.Lock()
			a.filter = expr
			a.mu.Unlock()
			if a.renderer != nil {
				a.renderCurrent()
			}
		}
		a.closeModal()
	})
	input.SetLabel("Filter (/re: !inv -f fuzzy -l label): ")
	a.showModal(input)
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
					gvr, ok = a.resolveDiscoveryResource(command.Value)
				}
				if !ok {
					a.showMessage("Unknown command", ":"+input.GetText())
					return
				}
				a.switchResource(ctx, gvr)
			case "namespace":
				a.switchNamespace(command.Value)
			case "context":
				if !a.switchContext(ctx, command.Value) {
					return
				}
			case "palette":
				a.showPalette()
				return
			case "quit":
				a.app.Stop()
				return
			case "port-forward-manager":
				a.showPortForwardManager()
				return
			case "flash":
				a.showFlashLog()
				return
			}
		}
		a.closeModal()
	})
	a.showModal(input)
}

func (a *App) switchNamespace(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}
	if namespace == "all" || namespace == "*" {
		namespace = ""
	}
	if a.options.Client == nil {
		a.options.Client = &cluster.Client{}
	}
	a.options.Client.Namespace = namespace
	a.renderCurrent()
}

// resolveDiscoveryResource maps a typed resource name (plural, singular, short
// name, or kind) to a GVR via API discovery, covering CRDs and any API group
// that is not in the static alias table.
func (a *App) resolveDiscoveryResource(name string) (schema.GroupVersionResource, bool) {
	if a.options.Client == nil || a.options.Client.Discovery == nil {
		return schema.GroupVersionResource{}, false
	}
	resolved, ok := cluster.ResolveResource(a.options.Client.Discovery, name)
	if !ok {
		return schema.GroupVersionResource{}, false
	}
	if !resolved.Namespaced {
		a.mu.Lock()
		if a.clusterScopedDyn == nil {
			a.clusterScopedDyn = map[schema.GroupVersionResource]bool{}
		}
		a.clusterScopedDyn[resolved.GVR] = true
		a.mu.Unlock()
	}
	return resolved.GVR, true
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
	a.crdRenderers = nil
	a.clusterScopedDyn = nil
	gvr := a.currentGVR
	a.mu.Unlock()
	if gvr.Resource == "" {
		gvr = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	}
	if !a.switchResource(ctx, gvr) {
		return false
	}
	// Repoint metrics at the new cluster so usage columns don't show stale data.
	a.mu.RLock()
	hadMetrics := a.metrics != nil
	a.mu.RUnlock()
	if hadMetrics && client.Dynamic != nil {
		a.startMetrics(ctx, cluster.NewMetricsPoller(client.Dynamic, 0))
	}
	a.nav.ResetToRoot()
	a.updateChrome()
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

// semanticColor maps a renderer's semantic color name to a concrete color
// through the active skin, falling back to the default foreground.
func (a *App) semanticColor(name string) tcell.Color {
	switch name {
	case "ok":
		return skinColor(a.skin.OK, tcell.ColorGreen)
	case "warn":
		return skinColor(a.skin.Warn, tcell.ColorYellow)
	case "error":
		return skinColor(a.skin.Error, tcell.ColorRed)
	case "muted":
		return skinColor(a.skin.Muted, tcell.ColorGray)
	case "accent":
		return skinColor(a.skin.Accent, tcell.ColorAqua)
	default:
		return skinColor(a.skin.FG, tcell.ColorWhite)
	}
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
			a.closeModal()
		})
		a.showModal(input)
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
		a.closeModal()
	})
	a.showModal(input)
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
		a.closeModal()
	})
	a.showModal(input)
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
		a.closeModal()
	})
	a.showModal(input)
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
		SetDoneFunc(func(_ int, _ string) { a.closeModal() })
	modal.SetTitle(" " + title + " ")
	a.showModal(modal)
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
	gvr := a.currentGVR
	a.mu.RUnlock()
	rows, visible := buildRows(a.renderer, objects, filter, sortCol, sortDesc, a.rowContext(gvr, objects))
	a.mu.Lock()
	a.visibleObjects = visible
	a.mu.Unlock()
	a.paintRows(a.renderer, rows, visible)
	a.reselectByKey(a.selKey(), visible)
	a.updateTitle()
}

// setTableCell writes a rendered cell, mapping its semantic color through the
// active skin and applying alignment.
func (a *App) setTableCell(row, col int, cell views.Cell) {
	tc := tview.NewTableCell(cell.Text)
	if cell.Color != "" {
		tc.SetTextColor(a.semanticColor(cell.Color))
	}
	if cell.Align != 0 {
		tc.SetAlign(cell.Align)
	}
	a.table.SetCell(row, col, tc)
}

// rowContext assembles the cross-object context for the current render. Metrics
// are wired in P1-2; today it returns relationship counts only.
func (a *App) rowContext(gvr schema.GroupVersionResource, objects []*unstructured.Unstructured) views.RowContext {
	rc := views.RowContext{}
	a.mu.RLock()
	poller := a.metrics
	a.mu.RUnlock()
	if poller != nil {
		pods, nodes, alloc, ok := poller.Snapshot()
		rc.PodMetrics = pods
		rc.NodeMetrics = nodes
		rc.MetricsOK = ok
		rc.Alloc = alloc
	}
	if gvr.Resource == "nodes" {
		rc.PodsByNode = a.podsByNode()
		rc.Alloc = nodeAllocatable(objects)
	}
	return rc
}

// nodeAllocatable extracts per-node allocatable CPU/mem/pods from node objects
// so the metrics renderer can compute % columns without another API call.
func nodeAllocatable(nodes []*unstructured.Unstructured) views.NodeAllocatable {
	alloc := views.NodeAllocatable{
		CPU:  map[string]int64{},
		Mem:  map[string]int64{},
		Pods: map[string]int64{},
	}
	for _, node := range nodes {
		name := node.GetName()
		if cpu, ok, _ := unstructured.NestedString(node.Object, "status", "allocatable", "cpu"); ok {
			if q, err := resource.ParseQuantity(cpu); err == nil {
				alloc.CPU[name] = q.MilliValue()
			}
		}
		if mem, ok, _ := unstructured.NestedString(node.Object, "status", "allocatable", "memory"); ok {
			if q, err := resource.ParseQuantity(mem); err == nil {
				alloc.Mem[name] = q.Value()
			}
		}
		if pods, ok, _ := unstructured.NestedString(node.Object, "status", "allocatable", "pods"); ok {
			if q, err := resource.ParseQuantity(pods); err == nil {
				alloc.Pods[name] = q.Value()
			}
		}
	}
	return alloc
}

// metricsSnapshotter is satisfied by the metrics poller. Kept as an interface
// so the UI has no hard dependency on the poller implementation and tests can
// inject fakes.
type metricsSnapshotter interface {
	Snapshot() (map[string]views.PodMetrics, map[string]views.NodeMetrics, views.NodeAllocatable, bool)
	Start(context.Context)
	Subscribe() <-chan struct{}
}

// startMetrics installs and starts a metrics poller, redrawing the current view
// after each successful poll.
func (a *App) startMetrics(ctx context.Context, poller metricsSnapshotter) {
	if poller == nil {
		return
	}
	a.mu.Lock()
	a.metrics = poller
	a.mu.Unlock()
	poller.Start(ctx)
	ticks := poller.Subscribe()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				a.app.QueueUpdateDraw(func() { a.renderCurrent() })
			}
		}
	}()
}

// podsByNode counts running pods per node from the pods informer if it is
// already synced. Returns nil when pods aren't cached (renderer shows n/a).
func (a *App) podsByNode() map[string]int {
	a.mu.RLock()
	cache := a.cache
	a.mu.RUnlock()
	if cache == nil {
		return nil
	}
	podGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	if !cache.HasInformer(podGVR) {
		return nil
	}
	pods, err := cache.List(podGVR, "", labels.Everything())
	if err != nil {
		return nil
	}
	counts := map[string]int{}
	for _, pod := range pods {
		node, _, _ := unstructured.NestedString(pod.Object, "spec", "nodeName")
		if node != "" {
			counts[node]++
		}
	}
	return counts
}

func (a *App) showDetail() {
	a.showDetailFor(a.selectedObject())
}

// showDescribe renders the resource manifest followed by the events that
// reference it, approximating `kubectl describe` / k9s `d`.
func (a *App) showDescribe() {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	detail := a.renderer.Detail(obj)
	detail.Body += "\n" + describeEventsSection(a.objectEvents(obj))
	a.showDetailContent(detail)
}

func (a *App) objectEvents(obj *unstructured.Unstructured) []corev1.Event {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	selector := fmt.Sprintf("involvedObject.name=%s", obj.GetName())
	list, err := a.options.Client.Clientset.CoreV1().Events(obj.GetNamespace()).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil
	}
	return list.Items
}

func describeEventsSection(events []corev1.Event) string {
	lines := []string{"EVENTS"}
	if len(events) == 0 {
		return strings.Join(append(lines, "  <none>"), "\n")
	}
	sort.SliceStable(events, func(i, j int) bool {
		return eventTime(events[i]).Before(eventTime(events[j]))
	})
	for _, event := range events {
		when := ""
		if t := eventTime(event); !t.IsZero() {
			when = time.Since(t).Round(time.Second).String() + " ago"
		}
		lines = append(lines, fmt.Sprintf("  %-8s %-20s %-12s %s", event.Type, event.Reason, when, event.Message))
	}
	return strings.Join(lines, "\n")
}

func eventTime(event corev1.Event) time.Time {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	return event.CreationTimestamp.Time
}

func (a *App) showDetailFor(obj *unstructured.Unstructured) {
	if obj == nil || a.renderer == nil {
		return
	}
	a.showDetailContent(a.renderer.Detail(obj))
}

func (a *App) showDetailContent(detail views.DetailContent) {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetText(detail.Body)
	view.SetTitle(" " + detail.Title + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		if event.Key() == tcell.KeyCtrlE || event.Rune() == 'a' {
			a.showManifestAI(view, detail.Body)
			return nil
		}
		return event
	})
	a.pushView(view, view, detail.Title, nil)
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

func (a *App) namespaceFor(client *cluster.Client, gvr schema.GroupVersionResource) string {
	a.mu.RLock()
	dynScoped := a.clusterScopedDyn[gvr]
	a.mu.RUnlock()
	if dynScoped {
		return ""
	}
	return namespaceFor(client, gvr)
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
	lines = append(lines, ":  command - Switch resource view (any discovered resource, e.g. :pods, :crds, :myresources)")
	lines = append(lines, ":ns <name>|all  namespace - Switch namespace (0 = all namespaces)")
	lines = append(lines, ":ctx <name>  context - Switch kube context")
	lines = append(lines, ":q  quit - Exit astrotui")
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

func buildRows(renderer views.Renderer, objects []*unstructured.Unstructured, filter string, sortCol int, sortDesc bool, rc views.RowContext) ([][]views.Cell, []*unstructured.Unstructured) {
	return BuildRowsCtx(renderer, objects, filter, sortCol, sortDesc, rc)
}

// BuildRows renders, filters (substring), and sorts rows without cross-object
// context. Retained for callers that have no metrics/relationship data.
func BuildRows(renderer views.Renderer, objects []*unstructured.Unstructured, filter string, sortCol int, sortDesc bool) ([][]views.Cell, []*unstructured.Unstructured) {
	return BuildRowsCtx(renderer, objects, filter, sortCol, sortDesc, views.RowContext{})
}

// BuildRowsCtx is BuildRows with cross-object RowContext (metrics, relationship
// counts) available to context-aware renderers.
func BuildRowsCtx(renderer views.Renderer, objects []*unstructured.Unstructured, filter string, sortCol int, sortDesc bool, rc views.RowContext) ([][]views.Cell, []*unstructured.Unstructured) {
	type rowObject struct {
		row []views.Cell
		obj *unstructured.Unstructured
	}
	pairs := make([]rowObject, 0, len(objects))
	rf := ParseRowFilter(filter)
	for _, obj := range objects {
		row := views.RenderRow(renderer, obj, rc)
		if !rf.Empty() && !rf.Matches(row, obj.GetLabels()) {
			continue
		}
		pairs = append(pairs, rowObject{row: row, obj: obj})
	}
	// Pre-sort by namespace/name so ties on the active sort column stay
	// deterministic regardless of cache iteration order.
	sort.SliceStable(pairs, func(i, j int) bool {
		if ns := strings.Compare(pairs[i].obj.GetNamespace(), pairs[j].obj.GetNamespace()); ns != 0 {
			return ns < 0
		}
		return pairs[i].obj.GetName() < pairs[j].obj.GetName()
	})
	sortColName := ""
	if header := renderer.Header(); sortCol >= 0 && sortCol < len(header) {
		sortColName = header[sortCol].Name
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if sortCol >= len(pairs[i].row) || sortCol >= len(pairs[j].row) {
			return false
		}
		c := views.CompareCellValuesFor(sortColName, pairs[i].row[sortCol].Text, pairs[j].row[sortCol].Text)
		if sortDesc {
			return c > 0
		}
		return c < 0
	})
	rows := make([][]views.Cell, len(pairs))
	visible := make([]*unstructured.Unstructured, len(pairs))
	for i, pair := range pairs {
		rows[i] = pair.row
		visible[i] = pair.obj
	}
	return rows, visible
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
