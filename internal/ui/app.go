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
	"k8s.io/client-go/dynamic"

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
	Client          *cluster.Client
	Config          config.Config
	SessionStore    *session.Store
	InitialState    session.State
	Skin            uitheme.Skin
	SkinUpdates     <-chan uitheme.Update
	PluginPaths     []string
	PluginExecutor  func(plugins.CommandPlan) error
	PluginPreviewer func(context.Context, plugins.CommandPlan) (string, error)
	ClientFactory   func(context.Context, cluster.ClientOptions) (*cluster.Client, error)
	Editor          actions.Editor
	ExecRunner      func(context.Context, actions.ExecTarget) error
	AttachRunner    func(context.Context, actions.AttachTarget) error
	PortForwards    *actions.PortForwardManager
	PortForwardRun  func(context.Context, actions.PortForwardSpec) error
	AIProvider      ai.LLMProvider
	LogStreamer     func(context.Context, logs.PodLogTarget, logs.RequestOptions, io.Writer) error
	Version         string
}

type App struct {
	options           Options
	app               *tview.Application
	table             *tview.Table
	cache             *cluster.ResourceCache
	resourceInfo      map[schema.GroupVersionResource]cluster.ResourceInfo
	resourceAliases   map[string]schema.GroupVersionResource
	resourceRenderers map[schema.GroupVersionResource]views.Renderer
	currentGVR        schema.GroupVersionResource
	objects           []*unstructured.Unstructured
	visibleObjects    []*unstructured.Unstructured
	filter            string
	sortCol           int
	sortDesc          bool
	mu                sync.RWMutex
	refreshMu         sync.Mutex
	resourceCancel    context.CancelFunc
	cacheCancel       context.CancelFunc
	runCtx            context.Context
	crashes           CrashReporter
	renderer          views.Renderer
	pluginFile        plugins.File
	skin              uitheme.Skin
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
	a.runCtx = ctx
	a.loadResourceInfo(a.options.Client)
	a.cache, a.cacheCancel = a.newCache(a.options.Client.Dynamic, a.options.Client.Namespace)
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
		if event.Rune() == 'H' {
			a.safe(a.showHealth)
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			a.safe(a.showDescribe)
			return nil
		}
		if event.Rune() == 'y' {
			a.safe(a.showDetail)
			return nil
		}
		if event.Rune() == 'v' {
			a.safe(a.showEventsForSelected)
			return nil
		}
		if event.Rune() == 'o' {
			a.safe(a.showOwnerForSelected)
			return nil
		}
		if event.Rune() == 'g' {
			a.safe(a.showRelatedForSelected)
			return nil
		}
		if event.Rune() == 'z' {
			a.safe(a.showResourceGraph)
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
		if event.Rune() == 'L' {
			a.safe(a.showLogOptionsPlan)
			return nil
		}
		if event.Rune() == 'x' {
			a.safe(a.showExecPlan)
			return nil
		}
		if event.Rune() == 'A' {
			a.safe(a.showAttachPlan)
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
		if event.Rune() == 'u' {
			a.safe(func() { a.showSuspendToggle(true) })
			return nil
		}
		if event.Rune() == 'U' {
			a.safe(func() { a.showSuspendToggle(false) })
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
	if a.cacheCancel != nil {
		a.cacheCancel()
	}
	if a.options.SessionStore != nil {
		_ = a.options.SessionStore.Save(a.sessionState())
	}
	return err
}

func (a *App) context() context.Context {
	if a.runCtx != nil {
		return a.runCtx
	}
	return context.Background()
}

func (a *App) newCache(client dynamic.Interface, namespace string) (*cluster.ResourceCache, context.CancelFunc) {
	cache := cluster.NewResourceCacheWithOptions(client, 30*time.Minute, cluster.CacheOptions{
		Namespace: namespace, AllNamespaces: namespace == "",
		MaxObjects: a.options.Config.Kubernetes.MaxCachedObjects, MaxObjectBytes: a.options.Config.Kubernetes.MaxCachedObjectBytes,
	})
	if a.runCtx == nil {
		return cache, nil
	}
	cacheCtx, cancel := context.WithCancel(a.runCtx)
	cache.Start(cacheCtx)
	return cache, cancel
}

func (a *App) loadResourceInfo(client *cluster.Client) {
	resources, _ := cluster.DiscoverResourceInfo(client)
	renderers := discoverCRDRenderers(client, resources)
	aliases := make(map[string]schema.GroupVersionResource)
	for _, resource := range resources {
		candidates := append([]string{resource.GVR.Resource, strings.ToLower(resource.Kind)}, resource.ShortNames...)
		for _, candidate := range candidates {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if candidate != "" {
				if _, exists := aliases[candidate]; !exists {
					aliases[candidate] = resource.GVR
				}
			}
		}
	}
	a.mu.Lock()
	a.resourceInfo = cluster.ResourceInfoIndex(resources)
	a.resourceAliases = aliases
	a.resourceRenderers = renderers
	a.mu.Unlock()
}

func discoverCRDRenderers(client *cluster.Client, resources []cluster.ResourceInfo) map[schema.GroupVersionResource]views.Renderer {
	out := make(map[schema.GroupVersionResource]views.Renderer)
	if client == nil || client.Dynamic == nil {
		return out
	}
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	var canListCRDs bool
	for _, resource := range resources {
		if resource.GVR == crdGVR && resource.Supports("list") {
			canListCRDs = true
			break
		}
	}
	if !canListCRDs {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := client.Dynamic.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for i := range list.Items {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[i].Object, &crd); err != nil {
			continue
		}
		for _, version := range crd.Spec.Versions {
			if !version.Served {
				continue
			}
			renderer := views.RendererForCRD(&crd, version.Name)
			out[renderer.GVR()] = renderer
		}
	}
	return out
}

func (a *App) rendererForGVR(gvr schema.GroupVersionResource) views.Renderer {
	a.mu.RLock()
	renderer := a.resourceRenderers[gvr]
	a.mu.RUnlock()
	if renderer != nil {
		return renderer
	}
	return rendererForGVR(gvr)
}

func (a *App) resolveResource(command string) (schema.GroupVersionResource, bool) {
	if gvr, ok := resourceCommand(command); ok {
		return gvr, true
	}
	normalized := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(command)), ":")
	a.mu.RLock()
	gvr, ok := a.resourceAliases[normalized]
	a.mu.RUnlock()
	return gvr, ok
}

func (a *App) resourceNamespaced(gvr schema.GroupVersionResource) bool {
	a.mu.RLock()
	info, ok := a.resourceInfo[gvr]
	a.mu.RUnlock()
	if ok {
		return info.Namespaced
	}
	return !clusterScoped(gvr.Resource)
}

func initialResourceGVR(state session.State) schema.GroupVersionResource {
	if gvr, ok := resourceCommand(state.LastView); ok {
		return gvr
	}
	return schema.GroupVersionResource{Version: "v1", Resource: "pods"}
}

func (a *App) sessionState() session.State {
	state := session.State{}
	a.mu.RLock()
	if a.options.Client != nil {
		state.Context = a.options.Client.Context
		state.Namespace = a.options.Client.Namespace
	}
	state.LastView = a.currentGVR.Resource
	state.LastFilter = a.filter
	a.mu.RUnlock()
	if state.LastView == "" {
		state.LastView = "pods"
	}
	state.AllNamespaces = state.Namespace == ""
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
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.RLock()
	current := a.currentGVR
	a.mu.RUnlock()
	if current != gvr {
		return
	}
	namespaced := a.resourceNamespaced(gvr)
	namespace := namespaceFor(client, gvr, namespaced)
	items, err := cache.ListFor(gvr, namespaced, namespace, labels.Everything())
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
				a.table.SetCell(row+1, col, tview.NewTableCell(tview.Escape(cell.Text)))
			}
		}
		a.updateTitle()
	})
}

func (a *App) updateTitle() {
	a.mu.RLock()
	gvr := a.currentGVR
	resource := gvr.Resource
	filter := a.filter
	cache := a.cache
	info, discovered := a.resourceInfo[gvr]
	client := cluster.Client{}
	if a.options.Client != nil {
		client = *a.options.Client
	}
	a.mu.RUnlock()
	if resource == "" {
		resource = "resources"
	}
	namespace := client.Namespace
	if namespace == "" {
		namespace = "all"
	}
	title := fmt.Sprintf(" astrotui [%s/%s] %s ", client.Context, namespace, resource)
	if filter != "" {
		title += fmt.Sprintf(" filter=%q ", filter)
	}
	if cache != nil {
		namespaced := !clusterScoped(gvr.Resource)
		if discovered {
			namespaced = info.Namespaced
		}
		state := cache.State(gvr, namespaced)
		if state.Phase != "" {
			title += fmt.Sprintf(" watch=%s ", state.Phase)
		}
	}
	if client.ImpersonateUser != "" {
		title += fmt.Sprintf(" as=%s ", client.ImpersonateUser)
	}
	a.table.SetTitle(title)
}

func (a *App) switchResource(ctx context.Context, gvr schema.GroupVersionResource) bool {
	renderer := a.rendererForGVR(gvr)
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
	namespaced := a.resourceNamespaced(gvr)
	cache.EnsureFor(gvr, namespaced)
	a.drawHeader(renderer)
	subscription := cache.SubscribeFor(gvr, namespaced)
	go func() {
		defer subscription.Cancel()
		cache.WaitForSyncFor(watchCtx, gvr, namespaced)
		a.refresh(cache, client, gvr, renderer)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-subscription.Events:
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
				gvr, ok := a.resolveResource(command.Value)
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
			case "auth-summary":
				a.showAuthSummary()
				return
			case "health":
				a.showHealth()
				return
			}
		}
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) switchNamespace(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "all" || namespace == "*" {
		namespace = ""
	}
	a.mu.Lock()
	if a.options.Client == nil {
		a.options.Client = &cluster.Client{}
	}
	clientSnapshot := *a.options.Client
	clientSnapshot.Namespace = namespace
	a.options.Client = &clientSnapshot
	gvr, renderer := a.currentGVR, a.renderer
	client := &clientSnapshot
	oldCacheCancel := a.cacheCancel
	if client.Dynamic != nil {
		a.cache, a.cacheCancel = a.newCache(client.Dynamic, namespace)
	}
	a.objects = nil
	a.visibleObjects = nil
	a.mu.Unlock()
	a.table.Clear()
	if renderer != nil {
		a.drawHeader(renderer)
	}
	a.updateTitle()
	if oldCacheCancel != nil {
		oldCacheCancel()
	}
	if a.cache != nil && renderer != nil && gvr.Resource != "" {
		a.switchResource(a.context(), gvr)
	}
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
	client, err := a.clientFactory()(ctx, cluster.ClientOptions{
		Context: contextName, Namespace: namespace, AllNamespaces: namespace == "",
		ImpersonateUser: a.options.Client.ImpersonateUser, ImpersonateUID: a.options.Client.ImpersonateUID,
		ImpersonateGroups: append([]string(nil), a.options.Client.ImpersonateGroups...),
	})
	if err != nil {
		a.showMessage("Context switch failed", err.Error())
		return false
	}
	a.loadResourceInfo(client)
	newCache, newCacheCancel := a.newCache(client.Dynamic, client.Namespace)
	a.mu.Lock()
	if a.resourceCancel != nil {
		a.resourceCancel()
		a.resourceCancel = nil
	}
	a.options.Client = client
	a.cache = newCache
	oldCacheCancel := a.cacheCancel
	a.cacheCancel = newCacheCancel
	gvr := a.currentGVR
	a.mu.Unlock()
	if oldCacheCancel != nil {
		oldCacheCancel()
	}
	if gvr.Resource == "" {
		gvr = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	}
	if !a.switchResource(a.context(), gvr) {
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

func (a *App) showAuthSummary() {
	a.mu.RLock()
	client := a.options.Client
	gvr := a.currentGVR
	a.mu.RUnlock()
	if client == nil {
		a.showMessage("Identity and RBAC", "Kubernetes client is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(a.context(), 8*time.Second)
	defer cancel()
	identity, identityErr := cluster.ReviewIdentity(ctx, client)
	namespace := namespaceFor(client, gvr, a.resourceNamespaced(gvr))
	allowed := make([]string, 0, 7)
	denied := make([]string, 0, 7)
	unknown := make([]string, 0, 7)
	for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete"} {
		ok, _, err := cluster.CanI(ctx, client, cluster.AccessRequest{Verb: verb, GVR: gvr, Namespace: namespace})
		switch {
		case err != nil:
			unknown = append(unknown, verb)
		case ok:
			allowed = append(allowed, verb)
		default:
			denied = append(denied, verb)
		}
	}
	displayNamespace := namespace
	if displayNamespace == "" {
		displayNamespace = "all / cluster-scoped"
	}
	lines := []string{
		"Context: " + client.Context,
		"Namespace: " + displayNamespace,
		"Resource: " + gvr.String(),
	}
	if identityErr == nil {
		lines = append(lines, "Username: "+identity.Username, "UID: "+identity.UID, "Groups: "+strings.Join(identity.Groups, ", "))
	} else {
		configured := client.ImpersonateUser
		if configured == "" {
			configured = "not reported"
		}
		lines = append(lines, "Username: "+configured, "Identity review: "+identityErr.Error())
	}
	lines = append(lines,
		"Allowed: "+strings.Join(allowed, ", "),
		"Denied: "+strings.Join(denied, ", "),
	)
	if len(unknown) > 0 {
		lines = append(lines, "Unknown: "+strings.Join(unknown, ", "))
	}
	a.showMessage("Identity and RBAC", strings.Join(lines, "\n"))
}

func (a *App) showHealth() {
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	if client == nil {
		a.showMessage("Cluster health", "Kubernetes client is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(a.context(), 12*time.Second)
	defer cancel()
	snapshot, err := cluster.ClusterHealth(ctx, client, client.Namespace)
	if err != nil {
		a.showMessage("Cluster health", err.Error())
		return
	}
	lines := []string{
		fmt.Sprintf("Nodes       %d/%d Ready", snapshot.NodesReady, snapshot.NodesTotal),
		fmt.Sprintf("Workloads   %d/%d Ready", snapshot.WorkloadsReady, snapshot.WorkloadsTotal),
		fmt.Sprintf("Pods        %d total  %d running  %d succeeded  %d pending  %d failed", snapshot.PodsTotal, snapshot.PodsRunning, snapshot.PodsSucceeded, snapshot.PodsPending, snapshot.PodsFailed),
		fmt.Sprintf("Restarts    %d", snapshot.ContainerRestarts),
		fmt.Sprintf("Warnings    %d recent events", snapshot.WarningEvents),
		"",
		"Problems",
	}
	if len(snapshot.Problems) == 0 {
		lines = append(lines, "No detected problems.")
	}
	for _, problem := range snapshot.Problems {
		lines = append(lines, fmt.Sprintf("%-8s %-48s %s", strings.ToUpper(problem.Severity), problem.Resource, problem.Message))
	}
	if len(snapshot.Errors) > 0 {
		lines = append(lines, "", "Unavailable checks", strings.Join(snapshot.Errors, "\n"))
	}
	a.showScrollableText(" Cluster health ", strings.Join(lines, "\n"))
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
	req := a.boundActionRequest("delete", a.renderer.GVR(), obj)
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
	req := a.boundActionRequest("edit", a.renderer.GVR(), obj)
	if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Edit) {
		input := tview.NewInputField().
			SetLabel(fmt.Sprintf("Type %s to edit %s/%s: ", a.options.Client.Context, req.Kind, req.ResourceName)).
			SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmEdit(req, obj, input.GetText())
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmEdit(req, obj, "")
}

func (a *App) confirmEdit(req actions.Request, obj *unstructured.Unstructured, confirmText string) {
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	if client == nil {
		a.showMessage("Edit failed", "Kubernetes client is unavailable")
		return
	}
	if err := safety.Authorize(a.options.Config, client.Context, safety.Edit, confirmText); err != nil {
		a.showMessage("Edit failed", err.Error())
		return
	}
	accessCtx, accessCancel := context.WithTimeout(a.context(), 2*time.Second)
	allowed, reason, err := cluster.CanI(accessCtx, client, accessRequestForAction(req))
	accessCancel()
	if err != nil {
		a.showMessage("Edit failed", "RBAC preflight failed: "+err.Error())
		return
	}
	if !allowed {
		if reason == "" {
			reason = "the active identity is not authorized"
		}
		a.showMessage("Edit failed", "RBAC denied: "+reason)
		return
	}
	var plan actions.EditPlan
	run := func() {
		plan, err = actions.PrepareEdit(obj, a.options.Editor)
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
	if len(plan.Patch) == 0 {
		a.showMessage("Edit unchanged", "No changes to apply")
		return
	}
	previewCtx, previewCancel := context.WithTimeout(a.context(), 15*time.Second)
	err = actions.PreviewEditPlan(previewCtx, client.Dynamic, req.GVR, plan)
	previewCancel()
	if err != nil {
		a.showMessage("Edit preview failed", err.Error())
		return
	}
	modal := tview.NewModal().
		SetText("Server dry-run succeeded.\n\n" + boundedPreview(plan.Diff, 64<<10)).
		AddButtons([]string{"Apply", "Cancel"})
	modal.SetTitle(" Edit dry-run preview ")
	modal.SetDoneFunc(func(index int, _ string) {
		if index != 0 {
			a.app.SetRoot(a.table, true)
			return
		}
		applyCtx, applyCancel := context.WithTimeout(a.context(), 30*time.Second)
		_, applyErr := actions.ApplyEditPlan(applyCtx, client.Dynamic, req.GVR, plan)
		applyCancel()
		if applyErr != nil {
			var conflict *actions.EditConflictError
			if errors.As(applyErr, &conflict) {
				a.showMessage("Edit conflict", conflict.Diff)
				return
			}
			a.showMessage("Edit failed", applyErr.Error())
			return
		}
		a.showMessage("Edited", fmt.Sprintf("edited %s/%s", req.Kind, req.ResourceName))
	})
	a.app.SetRoot(modal, false)
}

func boundedPreview(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n… preview truncated"
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
	req := a.boundActionRequest("scale", a.renderer.GVR(), obj)
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
	req := a.boundActionRequest("restart", a.renderer.GVR(), obj)
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

func (a *App) showSuspendToggle(suspend bool) {
	obj := a.selectedObject()
	if obj == nil || a.renderer == nil {
		return
	}
	name := "resume"
	if suspend {
		name = "suspend"
	}
	req := a.boundActionRequest(name, a.renderer.GVR(), obj)
	if req.Kind != "Job" && req.Kind != "CronJob" {
		a.showMessage("Action unavailable", fmt.Sprintf("%s is implemented for Job and CronJob", name))
		return
	}
	successTitle := "Resumed"
	if suspend {
		successTitle = "Suspended"
	}
	if safety.RequiresConfirmation(a.options.Config, a.currentContext(), safety.Patch) {
		a.showTypedAction(req, safety.Patch, successTitle, name+" failed")
		return
	}
	a.runAction(req, successTitle, name+" failed")
}

func scalableKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet"
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
		a.app.SetRoot(a.table, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) runAction(req actions.Request, successTitle, failureTitle string) {
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	if client == nil {
		a.showMessage(failureTitle, "Kubernetes client is unavailable")
		return
	}
	executor := actions.Executor{
		Clientset:      client.Clientset,
		Dynamic:        client.Dynamic,
		Config:         a.options.Config,
		CurrentContext: client.Context,
		RequireTarget:  true,
	}
	accessCtx, cancel := context.WithTimeout(a.context(), 2*time.Second)
	allowed, reason, err := cluster.CanI(accessCtx, client, accessRequestForAction(req))
	cancel()
	if err != nil {
		a.showMessage(failureTitle, "RBAC preflight failed: "+err.Error())
		return
	}
	if !allowed {
		if reason == "" {
			reason = "the active identity is not authorized"
		}
		a.showMessage(failureTitle, "RBAC denied: "+reason)
		return
	}
	previewCtx, previewCancel := context.WithTimeout(a.context(), 15*time.Second)
	preview, err := executor.Preview(previewCtx, req)
	previewCancel()
	if err != nil {
		a.showMessage(failureTitle, err.Error())
		return
	}
	body := preview.Summary
	if preview.Patch != "" {
		body += "\n\nPatch:\n" + preview.Patch
	}
	modal := tview.NewModal().SetText(body).AddButtons([]string{"Apply", "Cancel"})
	modal.SetTitle(" Dry-run preview ")
	modal.SetDoneFunc(func(index int, _ string) {
		if index != 0 {
			a.app.SetRoot(a.table, true)
			return
		}
		runCtx, runCancel := context.WithTimeout(a.context(), 30*time.Second)
		result, runErr := executor.Run(runCtx, req)
		runCancel()
		if runErr != nil {
			a.showMessage(failureTitle, runErr.Error())
			return
		}
		a.showMessage(successTitle, result.Message)
	})
	a.app.SetRoot(modal, false)
}

func accessRequestForAction(req actions.Request) cluster.AccessRequest {
	verb := "patch"
	if req.Name == "delete" {
		verb = "delete"
	}
	return cluster.AccessRequest{Verb: verb, GVR: req.Target.GVR, Namespace: req.Target.Namespace, Name: req.Target.Name}
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
			a.table.SetCell(row+1, col, tview.NewTableCell(tview.Escape(cell.Text)))
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
		SetDynamicColors(false).
		SetScrollable(true).
		SetText(detail.Body)
	view.SetTitle(" " + detail.Title + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		if event.Key() == tcell.KeyCtrlE || event.Rune() == 'a' {
			a.showManifestAIForObject(view, detail.Body, obj)
			return nil
		}
		if event.Rune() == 'v' {
			a.showEventsForSelected()
			return nil
		}
		if event.Rune() == 'o' {
			a.showOwnerForSelected()
			return nil
		}
		if event.Rune() == 'g' {
			a.showRelatedForSelected()
			return nil
		}
		if event.Rune() == 'z' {
			a.showResourceGraph()
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) namespaceNames() []string {
	names := map[string]struct{}{"all": {}}
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

func namespaceFor(client *cluster.Client, _ schema.GroupVersionResource, namespaced bool) string {
	if !namespaced {
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
	lines = append(lines, ":auth  identity - Show active server identity and RBAC verbs for the current resource")
	lines = append(lines, ":health  pulses - Show bounded cluster health and problem summary")
	lines = append(lines, "y/v/o/g/z  navigation - YAML, events, owner, related resources, and xray graph")
	lines = append(lines, "L  advanced logs - Configure tail, since, previous, timestamps, and follow")
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
		Target: actions.Target{
			GVR:             gvr,
			Kind:            kind,
			Namespace:       obj.GetNamespace(),
			Name:            obj.GetName(),
			UID:             obj.GetUID(),
			ResourceVersion: obj.GetResourceVersion(),
		},
		Name:         name,
		Kind:         kind,
		Namespace:    obj.GetNamespace(),
		ResourceName: obj.GetName(),
		GVR:          gvr,
	}
}

func (a *App) boundActionRequest(name string, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) actions.Request {
	req := actionRequest(name, gvr, obj)
	req.Target.Context = a.currentContext()
	return req
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
