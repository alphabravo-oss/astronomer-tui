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
}

type App struct {
	options          Options
	app              *tview.Application
	table            *tview.Table
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
			a.safe(a.showDescribe)
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
		if event.Key() == tcell.KeyCtrlD {
			a.safe(a.showDelete)
			return nil
		}
		if event.Rune() == 'd' {
			a.safe(a.showDescribe)
			return nil
		}
		if event.Rune() == 'y' {
			a.safe(a.showDetail)
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
		if event.Rune() == '0' {
			a.safe(func() { a.switchNamespace("all") })
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
	namespace := a.namespaceFor(client, gvr)
	items, err := cache.List(gvr, namespace, labels.Everything())
	if err != nil {
		return
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
		a.table.Clear()
		a.drawHeader(renderer)
		for row, cells := range rows {
			for col, cell := range cells {
				a.setTableCell(row+1, col, cell)
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
	title := fmt.Sprintf(" astrotui [%s/%s] %s ", a.options.Client.Context, displayNamespace(a.options.Client.Namespace), resource)
	a.mu.RLock()
	filter := a.filter
	a.mu.RUnlock()
	if filter != "" {
		title += fmt.Sprintf(" filter=%q ", filter)
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
		SetText(HelpText(a.actionCatalog())).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) { a.app.SetRoot(a.table, true) })
	a.app.SetRoot(modal, false)
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
	gvr := a.currentGVR
	a.mu.RUnlock()
	rows, visible := buildRows(a.renderer, objects, filter, sortCol, sortDesc, a.rowContext(gvr, objects))
	a.mu.Lock()
	a.visibleObjects = visible
	a.mu.Unlock()
	a.table.Clear()
	a.drawHeader(a.renderer)
	for row, cells := range rows {
		for col, cell := range cells {
			a.setTableCell(row+1, col, cell)
		}
	}
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
	}
	return rc
}

// metricsSnapshotter is satisfied by the metrics poller (wired in P1). Kept as
// an interface so the UI has no hard dependency on the poller implementation
// and tests can inject fakes.
type metricsSnapshotter interface {
	Snapshot() (map[string]views.PodMetrics, map[string]views.NodeMetrics, views.NodeAllocatable, bool)
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
	query := strings.ToLower(strings.TrimSpace(filter))
	for _, obj := range objects {
		row := views.RenderRow(renderer, obj, rc)
		if query != "" && !rowMatches(row, query) {
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
	sort.SliceStable(pairs, func(i, j int) bool {
		if sortCol >= len(pairs[i].row) || sortCol >= len(pairs[j].row) {
			return false
		}
		c := views.CompareCellValues(pairs[i].row[sortCol].Text, pairs[j].row[sortCol].Text)
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
