package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

var errTestExecShellMissing = errors.New("shell missing")

type captureProvider struct {
	requests chan ai.Request
}

func (c captureProvider) Complete(_ context.Context, _ ai.Request) (ai.Response, error) {
	return ai.Response{}, nil
}

func (c captureProvider) Stream(_ context.Context, req ai.Request) (<-chan ai.Chunk, error) {
	c.requests <- req
	chunks := make(chan ai.Chunk, 1)
	chunks <- ai.Chunk{Done: true}
	close(chunks)
	return chunks, nil
}

func TestHelpTextUsesActionCatalog(t *testing.T) {
	text := HelpText(actions.CatalogFor("Pod"))
	for _, want := range []string{"exec", "logs", "port-forward", "Ctrl+P", "enter", "filter", "sort", "delete", "command"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected help text to contain %q: %s", want, text)
		}
	}
	for _, notWant := range []string{"scale", "restart"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("did not expect pod help text to contain %q: %s", notWant, text)
		}
	}
}

func TestAppAppliesSkinToTableHeader(t *testing.T) {
	app := New(Options{Skin: uitheme.Skin{
		Name:      "test",
		FG:        "white",
		BG:        "black",
		Accent:    "green",
		Subtle:    "gray",
		Selection: "blue",
	}})
	app.drawHeader(views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME"}})
	cell := app.table.GetCell(0, 0)
	if cell == nil {
		t.Fatal("expected header cell")
	}
	fg, _, _ := cell.Style.Decompose()
	if fg != tcell.ColorGreen {
		t.Fatalf("expected green header cell, got %#v", cell)
	}
}

func TestAppAppliesLiveSkinUpdate(t *testing.T) {
	app := New(Options{
		Client: &cluster.Client{Context: "kind-kind", Namespace: "default"},
		Skin: uitheme.Skin{
			Name:      "old",
			FG:        "white",
			BG:        "black",
			Accent:    "green",
			Subtle:    "gray",
			Selection: "blue",
		},
	})
	app.renderer = views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME"}}
	app.applySkinUpdate(uitheme.Skin{
		Name:      "new",
		FG:        "white",
		BG:        "black",
		Accent:    "yellow",
		Subtle:    "gray",
		Selection: "blue",
	})
	cell := app.table.GetCell(0, 0)
	if cell == nil {
		t.Fatal("expected header cell")
	}
	fg, _, _ := cell.Style.Decompose()
	if fg != tcell.ColorYellow {
		t.Fatalf("expected yellow header after skin update, got %#v", cell)
	}
}

func TestResourceCommandAliases(t *testing.T) {
	for input, want := range map[string]string{
		":pods":       "pods",
		"po":          "pods",
		"deployments": "deployments",
		":deployment": "deployments",
		"dp":          "deployments",
		"svc":         "services",
		"cm":          "configmaps",
		"secret":      "secrets",
		"ns":          "namespaces",
		"sts":         "statefulsets",
		"ds":          "daemonsets",
		"cj":          "cronjobs",
		"ing":         "ingresses",
		"pvc":         "persistentvolumeclaims",
		"crb":         "clusterrolebindings",
	} {
		gvr, ok := resourceCommand(input)
		if !ok || gvr.Resource != want {
			t.Fatalf("%q => %#v %v, want %s", input, gvr, ok, want)
		}
	}
	if _, ok := resourceCommand("nope"); ok {
		t.Fatal("unexpected command match")
	}
}

func TestExplicitGVRCommandUsesGenericRenderer(t *testing.T) {
	gvr, ok := resourceCommand(":example.com/v1/widgets")
	if !ok || gvr.Group != "example.com" || gvr.Version != "v1" || gvr.Resource != "widgets" {
		t.Fatalf("unexpected explicit GVR %#v %v", gvr, ok)
	}
	renderer := rendererForGVR(gvr)
	if renderer == nil || renderer.GVR() != gvr {
		t.Fatalf("expected generic renderer for explicit GVR, got %#v", renderer)
	}
	core, ok := resourceCommand(":v1/widgets")
	if !ok || core.Group != "" || core.Version != "v1" || core.Resource != "widgets" {
		t.Fatalf("unexpected core explicit GVR %#v %v", core, ok)
	}
}

func TestParseNamespaceCommand(t *testing.T) {
	cmd, ok := parseCommand(":namespace kube-system")
	if !ok || cmd.Kind != "namespace" || cmd.Value != "kube-system" {
		t.Fatalf("unexpected command %#v %v", cmd, ok)
	}
	cmd, ok = parseCommand(":ns default")
	if !ok || cmd.Kind != "namespace" || cmd.Value != "default" {
		t.Fatalf("unexpected command %#v %v", cmd, ok)
	}
	cmd, ok = parseCommand(":pods")
	if !ok || cmd.Kind != "resource" || cmd.Value != "pods" {
		t.Fatalf("unexpected command %#v %v", cmd, ok)
	}
	cmd, ok = parseCommand(":pf")
	if !ok || cmd.Kind != "port-forward-manager" {
		t.Fatalf("unexpected command %#v %v", cmd, ok)
	}
	cmd, ok = parseCommand(":bench")
	if !ok || cmd.Kind != "benchmark-manager" {
		t.Fatalf("unexpected benchmark command %#v %v", cmd, ok)
	}
	if _, ok := parseCommand(":namespace"); ok {
		t.Fatal("namespace command should require a value")
	}
	auth, ok := parseCommand(":auth")
	if !ok || auth.Kind != "auth-summary" {
		t.Fatalf("unexpected auth command %#v %v", auth, ok)
	}
}

func TestInitialResourceGVRUsesSessionState(t *testing.T) {
	gvr := initialResourceGVR(session.State{LastView: "deployments"})
	if gvr.Group != "apps" || gvr.Resource != "deployments" {
		t.Fatalf("unexpected restored resource %#v", gvr)
	}
	gvr = initialResourceGVR(session.State{LastView: "not-a-resource"})
	if gvr.Resource != "pods" {
		t.Fatalf("expected pods fallback, got %#v", gvr)
	}
}

func TestSessionStateCapturesCurrentViewAndFilter(t *testing.T) {
	app := New(Options{Client: &cluster.Client{Context: "kind-kind", Namespace: "default"}})
	app.currentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	app.filter = "api"
	got := app.sessionState()
	want := session.State{Context: "kind-kind", Namespace: "default", LastView: "deployments", LastFilter: "api"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestActionCatalogUsesSelectedObjectKind(t *testing.T) {
	app := New(Options{})
	deployment := namedObject("api", "default")
	deployment.SetKind("Deployment")
	app.visibleObjects = []*unstructured.Unstructured{deployment}
	app.table.Select(1, 0)
	var names []string
	for _, action := range app.actionCatalog() {
		names = append(names, action.Name)
	}
	if !containsString(names, "scale") || !containsString(names, "restart") || !containsString(names, "logs") {
		t.Fatalf("expected deployment actions, got %#v", names)
	}
	if containsString(names, "exec") {
		t.Fatalf("did not expect pod exec action for deployment, got %#v", names)
	}
}

func TestPopulatePaletteListGroupsCategories(t *testing.T) {
	list := tview.NewList()
	entriesByRow := populatePaletteList(list, []palette.Match{
		{Entry: palette.Entry{ID: "action:delete", Title: "delete", Category: palette.Actions}},
		{Entry: palette.Entry{ID: "resource:pods", Title: ":pods", Category: palette.Resources}},
		{Entry: palette.Entry{ID: "namespace:default", Title: "default", Category: palette.Namespaces}},
	}, func(palette.Entry) bool { return true })
	if list.GetItemCount() != 6 {
		t.Fatalf("expected three category headers and three entries, got %d", list.GetItemCount())
	}
	first, _ := list.GetItemText(0)
	if !strings.Contains(first, string(palette.Resources)) {
		t.Fatalf("expected resources group first, got %q", first)
	}
	if entriesByRow[1].ID != "resource:pods" || entriesByRow[3].ID != "action:delete" || entriesByRow[5].ID != "namespace:default" {
		t.Fatalf("unexpected row mapping %#v", entriesByRow)
	}
}

func TestAIPaletteEntry(t *testing.T) {
	app := New(Options{})
	entry, ok := app.aiPaletteEntry("> restart api deployment")
	if !ok || entry.Category != palette.AI || entry.Payload != "restart api deployment" {
		t.Fatalf("unexpected ai entry %#v %v", entry, ok)
	}
	if _, ok := app.aiPaletteEntry("restart api deployment"); ok {
		t.Fatal("expected plain query to use normal fuzzy search")
	}
}

func TestExecuteAIEntryUsesProvider(t *testing.T) {
	app := New(Options{AIProvider: ai.MockProvider{Response: "clarify namespace"}})
	if !app.executePaletteEntry(palette.Entry{ID: "ai:nl-action", Title: "restart api", Category: palette.AI, Payload: "restart api"}) {
		t.Fatal("expected AI entry execution")
	}
}

func TestParseAIActionCall(t *testing.T) {
	if action, ok := parseAIActionCall("```json\n{\"action\":\"describe\"}\n```"); !ok || action != "describe" {
		t.Fatalf("unexpected action %q %v", action, ok)
	}
	if action, ok := parseAIActionCall("{\"verb\":\"restart\"}"); !ok || action != "restart" {
		t.Fatalf("unexpected verb %q %v", action, ok)
	}
	if _, ok := parseAIActionCall("which namespace?"); ok {
		t.Fatal("expected clarifying text not to parse as an action")
	}
}

func TestResourceAliasEntries(t *testing.T) {
	entries := ResourceAliasEntries()
	for _, want := range []string{":pods", ":deployments", ":services", ":statefulsets"} {
		var found bool
		for _, entry := range entries {
			if entry == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s in %#v", want, entries)
		}
	}
}

func TestEveryResourceAliasHasRenderer(t *testing.T) {
	for _, alias := range ResourceAliases() {
		if renderer := rendererForGVR(alias.GVR); renderer == nil {
			t.Fatalf("missing renderer for aliases %#v gvr=%s", alias.Aliases, alias.GVR)
		}
	}
}

func TestDeleteRequest(t *testing.T) {
	obj := namedObject("api", "default")
	obj.SetKind("Pod")
	req := deleteRequest(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, obj)
	if req.Name != "delete" || req.Kind != "Pod" || req.Namespace != "default" || req.ResourceName != "api" || req.GVR.Resource != "pods" {
		t.Fatalf("unexpected request %#v", req)
	}
}

func TestActionRequestForWorkload(t *testing.T) {
	obj := namedObject("api", "default")
	obj.SetKind("Deployment")
	req := actionRequest("restart", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, obj)
	if req.Name != "restart" || req.Kind != "Deployment" || req.GVR.Group != "apps" {
		t.Fatalf("unexpected request %#v", req)
	}
}

func TestDiscoveryAliasesAndVerbsDriveAvailableActions(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	app := New(Options{})
	app.currentGVR = gvr
	app.resourceAliases = map[string]schema.GroupVersionResource{"wdg": gvr, "widgets": gvr}
	app.resourceInfo = map[schema.GroupVersionResource]cluster.ResourceInfo{
		gvr: {GVR: gvr, Kind: "Widget", Namespaced: true, Verbs: map[string]struct{}{"get": {}, "patch": {}}},
	}
	resolved, ok := app.resolveResource("wdg")
	if !ok || resolved != gvr {
		t.Fatalf("expected discovered short name, got %v %v", resolved, ok)
	}
	for _, action := range app.actionCatalog() {
		if action.Name == "delete" {
			t.Fatal("delete should be hidden when discovery does not advertise it")
		}
	}
}

func TestRelatedTargetsForDeployment(t *testing.T) {
	app := New(Options{})
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "web", "namespace": "default"},
		"spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "web", "tier": "api"}}},
	}}
	targets := app.relatedTargets(obj)
	if len(targets) != 2 || targets[0].GVR.Resource != "replicasets" || targets[1].GVR.Resource != "pods" || targets[1].Filter != "app=web,tier=api" {
		t.Fatalf("unexpected related targets %#v", targets)
	}
}

func TestDescribeTextIncludesSummaryAndOwners(t *testing.T) {
	controller := true
	obj := podObject("api", "default")
	obj.SetAPIVersion("v1")
	obj.SetKind("Pod")
	obj.SetOwnerReferences([]metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-123", Controller: &controller}})
	text := describeText(obj, views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}})
	if !strings.Contains(text, "Pod") || !strings.Contains(text, "ReplicaSet/api-123") || !strings.Contains(text, "Summary") {
		t.Fatalf("unexpected describe text %q", text)
	}
}

func TestLogEntryPodRemovesContainerSuffix(t *testing.T) {
	if got := logEntryPod("api/sidecar"); got != "api" {
		t.Fatalf("unexpected pod name %q", got)
	}
}

func TestControllerOwnerPrefersControllerReference(t *testing.T) {
	controller, other := true, false
	owner, ok := controllerOwner([]metav1.OwnerReference{{Name: "ignored", Controller: &other}, {Name: "web", Kind: "Deployment", Controller: &controller}})
	if !ok || owner.Name != "web" {
		t.Fatalf("unexpected controller owner %#v %v", owner, ok)
	}
}

func TestDiscoverCRDRendererUsesPrinterColumns(t *testing.T) {
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": "widgets.example.com"},
		"spec": map[string]any{
			"group": "example.com", "scope": "Namespaced", "names": map[string]any{"plural": "widgets", "kind": "Widget"},
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true, "schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object"}}, "additionalPrinterColumns": []any{map[string]any{"name": "Phase", "type": "string", "jsonPath": ".status.phase"}}}},
		},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"}, crd)
	renderers := discoverCRDRenderers(&cluster.Client{Dynamic: client}, []cluster.ResourceInfo{{GVR: crdGVR, Verbs: map[string]struct{}{"list": {}}}})
	renderer := renderers[schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}]
	if renderer == nil {
		t.Fatal("expected discovered CRD renderer")
	}
	headers := renderer.Header()
	if len(headers) != 3 || headers[0].Name != "NAME" || headers[1].Name != "NAMESPACE" || headers[2].Name != "PHASE" {
		t.Fatalf("unexpected CRD headers %#v", headers)
	}
}

func TestExecutePaletteNamespaceEntry(t *testing.T) {
	app := New(Options{})
	if !app.executePaletteEntry(palette.Entry{ID: "namespace:kube-system", Title: "kube-system", Category: palette.Namespaces}) {
		t.Fatal("expected namespace entry execution")
	}
	if app.options.Client.Namespace != "kube-system" {
		t.Fatalf("expected namespace switch, got %q", app.options.Client.Namespace)
	}
}

func TestSwitchNamespaceClearsStaleSelection(t *testing.T) {
	app := New(Options{Client: &cluster.Client{Context: "dev", Namespace: "old"}})
	app.currentGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	app.renderer = views.GenericRenderer{Resource: app.currentGVR}
	app.objects = []*unstructured.Unstructured{namedObject("api", "old")}
	app.visibleObjects = append([]*unstructured.Unstructured(nil), app.objects...)
	app.switchNamespace("new")
	if app.options.Client.Namespace != "new" || len(app.objects) != 0 || len(app.visibleObjects) != 0 {
		t.Fatalf("stale selection survived namespace switch: %#v", app.visibleObjects)
	}
}

func TestPaletteEntriesIncludeDiscoveredNamespaces(t *testing.T) {
	app := New(Options{Client: &cluster.Client{
		Namespace: "default",
		Clientset: kubefake.NewSimpleClientset(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		),
	}})
	entries := app.paletteEntries()
	for _, want := range []string{"namespace:default", "namespace:kube-system"} {
		var found bool
		for _, entry := range entries {
			if entry.ID == want && entry.Category == palette.Namespaces {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s in palette entries", want)
		}
	}
}

func TestExecutePaletteContextEntryRebuildsClient(t *testing.T) {
	var got cluster.ClientOptions
	app := New(Options{
		Client: &cluster.Client{Context: "old", Namespace: "kube-system"},
		ClientFactory: func(_ context.Context, opts cluster.ClientOptions) (*cluster.Client, error) {
			got = opts
			return &cluster.Client{
				Context:   opts.Context,
				Namespace: opts.Namespace,
				Dynamic:   dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
			}, nil
		},
	})
	app.currentGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	if !app.executePaletteEntry(palette.Entry{ID: "context:prod", Title: "prod", Category: palette.Contexts}) {
		t.Fatal("expected context entry execution")
	}
	if got.Context != "prod" || got.Namespace != "kube-system" {
		t.Fatalf("unexpected client factory options %#v", got)
	}
	if app.options.Client.Context != "prod" || app.options.Client.Namespace != "kube-system" {
		t.Fatalf("unexpected client %#v", app.options.Client)
	}
	if app.cache == nil {
		t.Fatal("expected cache rebuild")
	}
}

func TestExecutePaletteActionEntry(t *testing.T) {
	app := New(Options{})
	if !app.executePaletteEntry(palette.Entry{ID: "action:describe", Title: "describe", Category: palette.Actions}) {
		t.Fatal("expected describe action to execute")
	}
	if app.executePaletteEntry(palette.Entry{ID: "action:unknown", Title: "unknown", Category: palette.Actions}) {
		t.Fatal("unexpected action execution")
	}
}

func TestExecutePaletteEditEntryRunsEditor(t *testing.T) {
	editor := &spyEditor{}
	clientset := kubefake.NewSimpleClientset()
	clientset.PrependReactor("create", "selfsubjectaccessreviews", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SelfSubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	obj := namedObject("api", "default")
	obj.SetKind("Pod")
	app := New(Options{
		Client: &cluster.Client{
			Context:   "kind-kind",
			Namespace: "default",
			Dynamic:   dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
			Clientset: clientset,
		},
		Editor: editor,
	})
	app.renderer = views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME"}}
	app.visibleObjects = []*unstructured.Unstructured{obj}
	app.table.Select(1, 0)
	if !app.executePaletteEntry(palette.Entry{ID: "action:edit", Title: "edit", Category: palette.Actions}) {
		t.Fatal("expected edit action to execute")
	}
	if editor.calls != 1 {
		t.Fatalf("expected editor call, got %d", editor.calls)
	}
}

func TestExecutePaletteOperationalActions(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "app",
			Image: "example",
			Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
		}}},
	})
	pod.SetKind("Pod")
	for _, action := range []string{"logs", "exec", "copy", "port-forward"} {
		app := New(Options{PortForwards: actions.NewPortForwardManager()})
		app.visibleObjects = []*unstructured.Unstructured{pod}
		app.table.Select(1, 0)
		if !app.executePaletteEntry(palette.Entry{ID: "action:" + action, Title: action, Category: palette.Actions}) {
			t.Fatalf("expected %s action to execute", action)
		}
	}
}

func TestCopyRunnerHonorsSafetyAndSelectedContainer(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	})
	pod.SetKind("Pod")
	cfg := config.Default()
	cfg.Contexts = map[string]config.ContextConfig{"dev": {Policy: "full"}, "locked": {Policy: "read-only"}}
	var got actions.CopySpec
	app := New(Options{
		Client: &cluster.Client{Context: "dev", Namespace: "default"}, Config: cfg,
		CopyRunner: func(_ context.Context, spec actions.CopySpec) error { got = spec; return nil },
	})
	app.visibleObjects = []*unstructured.Unstructured{pod}
	app.table.Select(1, 0)
	spec := actions.CopySpec{Direction: actions.CopyFromPod, Namespace: "default", Pod: "api", Container: "app", RemotePath: "/tmp/data", LocalPath: t.TempDir()}
	app.runCopy("", spec)
	if got.Pod != "api" || got.Container != "app" || got.Direction != actions.CopyFromPod {
		t.Fatalf("unexpected copy target %#v", got)
	}
	app.options.Client.Context = "locked"
	got = actions.CopySpec{}
	app.runCopy("", spec)
	if got.Pod != "" {
		t.Fatalf("read-only context ran copy %#v", got)
	}
}

func TestBenchmarkInputsStayBoundToLoopbackForward(t *testing.T) {
	forward := actions.PortForward{ID: "pf-1", Local: 18080, Resource: "pod/api"}
	spec, err := benchmarkSpecFromInputs(forward, "/health?full=1", "25", "5", "2s")
	if err != nil {
		t.Fatal(err)
	}
	if spec.URL != "http://127.0.0.1:18080/health?full=1" || spec.Requests != 25 || spec.Concurrency != 5 || spec.Timeout != 2*time.Second {
		t.Fatalf("unexpected benchmark spec %#v", spec)
	}
	for _, unsafe := range []string{"https://example.com/", "//example.com/", "relative"} {
		if _, err := benchmarkSpecFromInputs(forward, unsafe, "10", "2", "1s"); err == nil {
			t.Fatalf("expected unsafe path rejection for %q", unsafe)
		}
	}
}

func TestRenderBenchmarkRowsAndSelection(t *testing.T) {
	table := tview.NewTable().SetSelectable(true, false)
	runs := []actions.Benchmark{{ID: "bench-2", Status: "running"}, {ID: "bench-1", Status: "completed", Result: actions.BenchmarkResult{Completed: 10}}}
	renderBenchmarkRows(table, runs)
	table.Select(1, 0)
	run, ok := benchmarkAt(table, runs)
	if !ok || run.ID != "bench-1" {
		t.Fatalf("unexpected selected benchmark %#v ok=%v", run, ok)
	}
}

func TestPortForwardInputParsingAndStart(t *testing.T) {
	stopped := make(chan struct{})
	app := New(Options{
		PortForwards: actions.NewPortForwardManager(),
		PortForwardRun: func(ctx context.Context, _ actions.PortForwardSpec) error {
			<-ctx.Done()
			close(stopped)
			return nil
		},
	})
	base := actions.PortForwardSpec{Namespace: "default", Resource: "pod/api", Local: 8080, Remote: 8080}
	spec, err := parsePortForwardSpec("18080:8080", base)
	if err != nil {
		t.Fatal(err)
	}
	app.startPortForwardSpec(spec)
	forwards := app.options.PortForwards.List()
	if len(forwards) != 1 || forwards[0].Local != 18080 || forwards[0].Remote != 8080 {
		t.Fatalf("unexpected forwards %#v", forwards)
	}
	if !app.options.PortForwards.Kill(forwards[0].ID) {
		t.Fatal("expected kill to stop managed forward")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for port-forward runner stop")
	}
	if _, err := parsePortForwardSpec("bad", base); err == nil {
		t.Fatal("expected parse error")
	}
	named, err := parsePortForwardSpec("18080:http", base)
	if err != nil || named.RemoteName != "http" || named.Remote != 0 || portForwardInput(named) != "18080:http" {
		t.Fatalf("unexpected named port parse %#v err=%v", named, err)
	}
}

func TestPortForwardAndPluginHonorReadOnlyPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Contexts = map[string]config.ContextConfig{"prod": {Policy: "read-only"}}
	called := false
	app := New(Options{
		Client:       &cluster.Client{Context: "prod", Namespace: "default"},
		Config:       cfg,
		PortForwards: actions.NewPortForwardManager(),
		PluginExecutor: func(plugins.CommandPlan) error {
			called = true
			return nil
		},
	})
	app.startPortForwardSpec(actions.PortForwardSpec{Namespace: "default", Resource: "pod/api", Local: 8080, Remote: 8080})
	if len(app.options.PortForwards.List()) != 0 {
		t.Fatal("read-only context started a port-forward")
	}
	app.executePluginEntry(palette.Entry{Title: "delete", Payload: plugins.Plugin{Command: "kubectl", Args: []string{"delete", "pod", "api"}}})
	if called {
		t.Fatal("read-only context executed a plugin")
	}
}

func TestExecPlanHonorsSafetyPolicy(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example"}}},
	})
	pod.SetKind("Pod")
	cfg := config.Default()
	cfg.Contexts = map[string]config.ContextConfig{"prod": {Policy: "confirm-destructive"}, "dev": {Policy: "read-only"}}
	app := New(Options{Client: &cluster.Client{Context: "prod", Namespace: "default"}, Config: cfg})
	app.visibleObjects = []*unstructured.Unstructured{pod}
	app.table.Select(1, 0)
	if _, err := app.execPlanLines("wrong"); err == nil {
		t.Fatal("expected typed confirmation error")
	}
	lines, err := app.execPlanLines("prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "kubectl exec") {
		t.Fatalf("unexpected exec plan %#v", lines)
	}
	app.options.Client.Context = "dev"
	if _, err := app.execPlanLines(""); err != safety.ErrReadOnly {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestExecRunnerUsesFirstTarget(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example"}}},
	})
	pod.SetKind("Pod")
	var got actions.ExecTarget
	app := New(Options{
		Client: &cluster.Client{Context: "dev", Namespace: "default"},
		ExecRunner: func(_ context.Context, target actions.ExecTarget) error {
			got = target
			return nil
		},
	})
	app.visibleObjects = []*unstructured.Unstructured{pod}
	app.table.Select(1, 0)
	app.confirmExecPlan("")
	if got.Pod != "api" || got.Container != "app" || len(got.Command) == 0 || got.Command[0] != "bash" {
		t.Fatalf("unexpected exec target %#v", got)
	}
}

func TestRunExecFallbackTriesNextShell(t *testing.T) {
	var commands []string
	targets := []actions.ExecTarget{
		{Command: []string{"bash"}},
		{Command: []string{"sh"}},
	}
	err := runExecFallback(context.Background(), func(_ context.Context, target actions.ExecTarget) error {
		commands = append(commands, target.Command[0])
		if target.Command[0] == "bash" {
			return errTestExecShellMissing
		}
		return nil
	}, targets)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(commands, ",") != "bash,sh" {
		t.Fatalf("unexpected commands %#v", commands)
	}
}

func TestExecTargetsAndPlanUsesPreferredContainer(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "example"},
			{Name: "sidecar", Image: "example"},
		}},
	})
	pod.SetKind("Pod")
	app := New(Options{Client: &cluster.Client{Context: "dev", Namespace: "default"}})
	app.visibleObjects = []*unstructured.Unstructured{pod}
	app.table.Select(1, 0)
	targets, lines, err := app.execTargetsAndPlan("", "sidecar")
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].Container != "sidecar" || !strings.Contains(lines[0], "-c sidecar") {
		t.Fatalf("unexpected targets=%#v lines=%#v", targets, lines)
	}
}

func TestPortForwardManagerRows(t *testing.T) {
	table := tview.NewTable().SetSelectable(true, false)
	forwards := []actions.PortForward{
		{ID: "default:pod/b:8081:8081", Namespace: "default", Resource: "pod/b", Local: 8081, Remote: 8081},
		{ID: "default:pod/a:8080:8080", Namespace: "default", Resource: "pod/a", Local: 8080, Remote: 8080},
	}
	renderPortForwardRows(table, forwards)
	table.Select(1, 0)
	if got := portForwardIDAt(table, forwards); got != "default:pod/a:8080:8080" {
		t.Fatalf("unexpected selected forward %q", got)
	}
	cell := table.GetCell(1, 2)
	if cell == nil || cell.Text != "pod/a" {
		t.Fatalf("expected sorted first forward row, got %#v", cell)
	}
}

func TestKillSelectedPortForward(t *testing.T) {
	table := tview.NewTable().SetSelectable(true, false)
	manager := actions.NewPortForwardManager()
	var stopped bool
	manager.Add(actions.NewPortForward(actions.PortForwardSpec{Namespace: "default", Resource: "pod/b", Local: 8081, Remote: 8081}, nil))
	manager.Add(actions.NewPortForward(actions.PortForwardSpec{Namespace: "default", Resource: "pod/a", Local: 8080, Remote: 8080}, func() { stopped = true }))
	renderPortForwardRows(table, manager.List())
	table.Select(1, 0)
	if !killSelectedPortForward(table, manager) {
		t.Fatal("expected selected forward to be killed")
	}
	if !stopped {
		t.Fatal("expected selected forward stop function to run")
	}
	if len(manager.List()) != 1 || manager.List()[0].ID != "default:pod/b:8081:8081" {
		t.Fatalf("unexpected remaining forwards %#v", manager.List())
	}
}

func TestSelectedLogTargetsForDeployment(t *testing.T) {
	deployment := unstructuredFrom(t, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
		},
	})
	deployment.SetKind("Deployment")
	app := New(Options{Client: &cluster.Client{Clientset: kubefake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "default", Labels: map[string]string{"app": "api"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example"}}},
		},
	)}})
	app.visibleObjects = []*unstructured.Unstructured{deployment}
	app.table.Select(1, 0)
	targets, err := app.selectedLogTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Pod != "api-1" || targets[0].Container != "app" {
		t.Fatalf("unexpected targets %#v", targets)
	}
}

func TestLogLineWriterAndRender(t *testing.T) {
	buffer := logs.NewBuffer(10)
	var appends int
	writer := newLogLineWriter("api", buffer, func() { appends++ })
	if _, err := writer.Write([]byte("first\nsecond")); err != nil {
		t.Fatal(err)
	}
	if appends != 1 {
		t.Fatalf("expected one complete line append, got %d", appends)
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	rendered := renderLogEntries(buffer.Entries(), true)
	visible := tview.Unescape(rendered)
	if !strings.Contains(visible, "[api] first") || !strings.Contains(visible, "[api] second") {
		t.Fatalf("unexpected rendered logs %q", rendered)
	}
	if !strings.Contains(rendered, "[-]") {
		t.Fatalf("expected colored pod prefixes, got %q", rendered)
	}
}

func TestLogSummaryContextUsesRecentEntries(t *testing.T) {
	buffer := logs.NewBuffer(10)
	buffer.Append("api", "old", time.Unix(1, 0))
	buffer.Append("api", "new", time.Unix(2, 0))
	got := logSummaryContext(buffer.Entries(), 1)
	if strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Fatalf("unexpected summary context %q", got)
	}
}

func TestLogAIChatFocusesQuestionInput(t *testing.T) {
	buffer := logs.NewBuffer(10)
	buffer.Append("api", "error happened", time.Unix(1, 0))
	app := New(Options{AIProvider: ai.MockProvider{Response: "answer"}})
	logView := tview.NewTextView()
	app.showLogAIChat(logView, buffer, "")
	if _, ok := app.app.GetFocus().(*tview.InputField); !ok {
		t.Fatalf("expected AI chat input focus, got %T", app.app.GetFocus())
	}
}

func TestLogContainerTargetAndWrapToggle(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app"},
			{Name: "sidecar"},
		}},
	}
	target := logTargetForPodContainer(pod, "sidecar")
	if target.Namespace != "default" || target.Pod != "api" || target.Container != "sidecar" {
		t.Fatalf("unexpected target %#v", target)
	}
	view := tview.NewTextView().SetWrap(true)
	wrap := true
	toggleLogWrap(view, &wrap)
	if wrap {
		t.Fatal("expected wrap to toggle off")
	}
}

func TestSelectedLogEntryAndOwnerGVR(t *testing.T) {
	buffer := logs.NewBuffer(10)
	buffer.Append("api-1", "first", time.Unix(1, 0))
	buffer.Append("api-2", "second", time.Unix(2, 0))
	view := tview.NewTextView()
	view.ScrollTo(1, 0)
	entry, ok := selectedLogEntry(view, buffer, "")
	if !ok || entry.Pod != "api-2" {
		t.Fatalf("unexpected selected entry %#v %v", entry, ok)
	}
	gvr, ok := ownerGVR("Deployment")
	if !ok || gvr.Resource != "deployments" || gvr.Group != "apps" {
		t.Fatalf("unexpected owner gvr %#v %v", gvr, ok)
	}
	if _, ok := ownerGVR("Unknown"); ok {
		t.Fatal("unexpected owner mapping")
	}
}

func TestPodForLogEntryUsesSelectedPod(t *testing.T) {
	pod := unstructuredFrom(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{
			Kind: "Deployment",
			Name: "api",
		}}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	})
	pod.SetKind("Pod")
	app := New(Options{})
	app.visibleObjects = []*unstructured.Unstructured{pod}
	app.table.Select(1, 0)
	got, err := app.podForLogEntry(context.Background(), logs.Entry{Pod: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "api" || len(got.OwnerReferences) != 1 {
		t.Fatalf("unexpected pod %#v", got)
	}
}

func TestLogSearchStateNavigation(t *testing.T) {
	state := newLogSearchState([]int{2, 5})
	if row, ok := state.Next(); !ok || row != 2 {
		t.Fatalf("unexpected next %d %v", row, ok)
	}
	if row, ok := state.Next(); !ok || row != 5 {
		t.Fatalf("unexpected second next %d %v", row, ok)
	}
	if row, ok := state.Next(); !ok || row != 2 {
		t.Fatalf("expected wrap next, got %d %v", row, ok)
	}
	if row, ok := state.Prev(); !ok || row != 5 {
		t.Fatalf("expected wrap prev, got %d %v", row, ok)
	}
}

func TestPodFailureContextIncludesSpecStatusAndEvents(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "bad:tag"}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}},
	}
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "api-pull", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api",
		},
		Type:          "Warning",
		Reason:        "Failed",
		Message:       "failed to pull image",
		LastTimestamp: metav1.Unix(2, 0),
	}
	app := New(Options{Client: &cluster.Client{Clientset: kubefake.NewSimpleClientset(event)}})
	spec, status, events, err := app.podFailureContext(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec, "bad:tag") || !strings.Contains(status, "ImagePullBackOff") || !strings.Contains(events, "failed to pull image") {
		t.Fatalf("spec=%s status=%s events=%s", spec, status, events)
	}
	if got := podFailureReason(pod); got != "ImagePullBackOff" {
		t.Fatalf("unexpected failure reason %q", got)
	}
}

func TestManifestAIDisabledHint(t *testing.T) {
	app := New(Options{})
	view := tview.NewTextView()
	app.showManifestAI(view, "kind: Pod")
	if !strings.Contains(view.GetText(true), "AI is disabled") {
		t.Fatalf("expected disabled hint, got %q", view.GetText(true))
	}
}

func TestSecretManifestAIRedactsData(t *testing.T) {
	requests := make(chan ai.Request, 1)
	app := New(Options{AIProvider: captureProvider{requests: requests}})
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "credentials"},
		"data":       map[string]any{"token": "c2VjcmV0"}, // #nosec G101 -- synthetic value verifies Secret redaction.
	}}
	app.showManifestAIForObject(tview.NewTextView(), "original", secret)
	select {
	case req := <-requests:
		if strings.Contains(req.Prompt, "c2VjcmV0") || !strings.Contains(req.Prompt, "<redacted>") {
			t.Fatalf("secret was not redacted: %s", req.Prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AI request")
	}
}

func TestExecutePalettePluginEntryPlansCommand(t *testing.T) {
	var got plugins.CommandPlan
	app := New(Options{PluginExecutor: func(plan plugins.CommandPlan) error {
		got = plan
		return nil
	}})
	app.options.Client = nil
	app.mu.Lock()
	app.visibleObjects = []*unstructured.Unstructured{namedObject("api", "default")}
	app.mu.Unlock()
	app.table.Select(1, 0)
	entry := palette.Entry{
		ID:       "plugin:logs",
		Title:    "logs",
		Category: palette.Plugins,
		Payload: plugins.Plugin{
			Command: "kubectl",
			Args:    []string{"logs", "$NAME", "-n", "$NAMESPACE"},
		},
	}
	if !app.executePaletteEntry(entry) {
		t.Fatal("expected plugin entry execution")
	}
	if got.Command != "kubectl" || strings.Join(got.Args, " ") != "logs api -n default" {
		t.Fatalf("unexpected plan %#v", got)
	}
}

func TestMutatingPluginRequiresPreviewBeforeExecution(t *testing.T) {
	var previewed, executed bool
	app := New(Options{
		PluginPreviewer: func(_ context.Context, plan plugins.CommandPlan) (string, error) {
			previewed = plan.Mutates
			return "server dry-run output", nil
		},
		PluginExecutor: func(plugins.CommandPlan) error {
			executed = true
			return nil
		},
	})
	entry := palette.Entry{Title: "delete", Payload: plugins.Plugin{
		Command: "kubectl", Args: []string{"delete", "pod", "api"}, Mutates: true,
		PreviewCommand: "kubectl", PreviewArgs: []string{"delete", "pod", "api", "--dry-run=server"},
	}}
	if !app.executePluginEntry(entry) || !previewed {
		t.Fatal("expected mutating plugin preview")
	}
	if executed {
		t.Fatal("mutating plugin executed before explicit Apply")
	}
}

func TestSelectedObjectMapsTableRowsAfterHeader(t *testing.T) {
	objects := []*unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "first"}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "second"}}},
	}
	if selectedObject(objects, 0) != nil {
		t.Fatal("header row should not map to an object")
	}
	got := selectedObject(objects, 2)
	if got == nil || got.GetName() != "second" {
		t.Fatalf("unexpected object %#v", got)
	}
	if selectedObject(objects, 3) != nil {
		t.Fatal("out-of-range row should not map to an object")
	}
}

func TestBuildRowsFiltersAndSortsVisibleObjects(t *testing.T) {
	renderer := views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAME", "NAMESPACE"}}
	worker := namedObject("worker", "default")
	api := namedObject("api", "default")
	db := namedObject("db", "storage")
	rows, visible := buildRows(renderer, []*unstructured.Unstructured{worker, api, db}, "default", 0, false)
	if len(rows) != 2 || len(visible) != 2 {
		t.Fatalf("rows=%#v visible=%#v", rows, visible)
	}
	if visible[0].GetName() != "api" || visible[1].GetName() != "worker" {
		t.Fatalf("expected sorted visible objects, got %s/%s", visible[0].GetName(), visible[1].GetName())
	}
	rows, visible = buildRows(renderer, []*unstructured.Unstructured{worker, api, db}, "", 0, true)
	if len(rows) != 3 || visible[0].GetName() != "worker" || visible[2].GetName() != "api" {
		t.Fatalf("expected descending sort, rows=%#v visible=%#v", rows, visible)
	}
	namespaceFirst := views.GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAMESPACE", "NAME"}}
	_, visible = buildRows(namespaceFirst, []*unstructured.Unstructured{worker, api}, "", 0, false)
	if visible[0].GetName() != "api" || visible[1].GetName() != "worker" {
		t.Fatalf("expected deterministic identity tie-breaker, got %s/%s", visible[0].GetName(), visible[1].GetName())
	}
}

func TestSwitchResourceAppliesCacheUpdates(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	watcher := watch.NewRaceFreeFake()
	watchStarted := make(chan struct{})
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), podObject("api", "default"))
	client.PrependWatchReactor("pods", func(ktesting.Action) (bool, watch.Interface, error) {
		select {
		case <-watchStarted:
		default:
			close(watchStarted)
		}
		return true, watcher, nil
	})
	app := New(Options{Client: &cluster.Client{
		Context:   "kind-kind",
		Namespace: "default",
		Dynamic:   client,
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.cache = cluster.NewResourceCache(client, time.Hour)
	app.cache.Start(ctx)
	if !app.switchResource(ctx, gvr) {
		t.Fatal("expected resource switch")
	}
	waitForVisibleNames(t, app, []string{"api"})
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	app.app.SetScreen(screen)
	app.app.SetRoot(app.table, true)
	done := make(chan error, 1)
	go func() { done <- app.app.Run() }()
	defer func() {
		app.app.Stop()
		if err := <-done; err != nil {
			t.Fatalf("tview run: %v", err)
		}
	}()
	select {
	case <-watchStarted:
	case <-ctx.Done():
		t.Fatal("watch did not start")
	}
	events := app.cache.Subscribe(gvr)
	watcher.Add(podObject("worker", "default"))
	select {
	case event := <-events:
		if event.Object.GetName() != "worker" {
			t.Fatalf("unexpected cache event %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("cache event did not arrive")
	}
	waitForVisibleNames(t, app, []string{"api", "worker"})
}

func TestSortColumnFromRune(t *testing.T) {
	if col, ok := sortColumnFromRune('@'); !ok || col != 1 {
		t.Fatalf("expected @ to map to column 1, got %d %v", col, ok)
	}
	if _, ok := sortColumnFromRune('2'); ok {
		t.Fatal("plain digits should not be treated as Shift+<n>")
	}
}

func TestClusterScoped(t *testing.T) {
	if !clusterScoped("namespaces") || !clusterScoped("nodes") || !clusterScoped("clusterroles") {
		t.Fatal("expected cluster-scoped resources")
	}
	if clusterScoped("pods") || clusterScoped("deployments") {
		t.Fatal("expected namespaced resources")
	}
}

type spyEditor struct {
	calls int
}

func (s *spyEditor) Edit(string) error {
	s.calls++
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func namedObject(name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	return obj
}

func podObject(name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"status": map[string]any{"phase": "Running"},
	}}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	return obj
}

func waitForVisibleNames(t *testing.T, app *App, want []string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var got []string
		for _, obj := range app.visibleObjectsSnapshot() {
			got = append(got, obj.GetName())
		}
		if strings.Join(got, ",") == strings.Join(want, ",") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var got []string
	for _, obj := range app.visibleObjectsSnapshot() {
		got = append(got, obj.GetName())
	}
	t.Fatalf("visible names got %#v want %#v", got, want)
}

func unstructuredFrom(t *testing.T, obj any) *unstructured.Unstructured {
	t.Helper()
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: data}
}
