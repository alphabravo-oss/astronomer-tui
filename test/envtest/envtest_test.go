package envtest

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	cachedmemory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	crenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestResourceCacheEnvtestWatchEventPropagatesWithin200ms(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	namespace := createNamespace(ctx, t, clientset, "astrotui-watch")
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	cache := cluster.NewResourceCache(client, 0)
	events := cache.Subscribe(gvr)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("pod informer did not sync")
	}
	pod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "api",
			"namespace": namespace,
			"labels":    map[string]any{"app": "api"},
		},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "main", "image": "registry.k8s.io/pause:3.9"}},
		},
	}}
	start := time.Now()
	if _, err := client.Resource(gvr).Namespace(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != cluster.EventAdded || event.Object.GetName() != "api" {
			t.Fatalf("unexpected event %#v", event)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("watch event took %s, exceeds 200ms", elapsed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watch event did not arrive within 200ms")
	}
}

func TestEnvtestCRDInstancesAppearInDynamicView(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	apiExt, err := apiextensionsclientset.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	namespace := createNamespace(ctx, t, clientset, "astrotui-crd")
	crd := widgetCRD()
	if _, err := apiExt.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForCRDEstablished(ctx, t, apiExt, crd.Name)
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	cache := cluster.NewResourceCache(dyn, 0)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("widget informer did not sync")
	}
	widget := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name":      "first",
			"namespace": namespace,
		},
		"spec": map[string]any{"size": "large"},
	}}
	if _, err := dyn.Resource(gvr).Namespace(namespace).Create(ctx, widget, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	var listed []*unstructured.Unstructured
	if err := wait.PollUntilContextTimeout(ctx, 25*time.Millisecond, time.Second, true, func(context.Context) (bool, error) {
		var listErr error
		listed, listErr = cache.List(gvr, namespace, nil)
		return len(listed) == 1, listErr
	}); err != nil {
		t.Fatal(err)
	}
	renderer := views.RendererForCRD(crd, "v1")
	headers := renderer.Header()
	row := renderer.Row(listed[0])
	if len(headers) != 2 || headers[0].Name != "NAME" || headers[1].Name != "SIZE" {
		t.Fatalf("unexpected CRD headers %#v", headers)
	}
	if row[0].Text != "first" || row[1].Text != "large" {
		t.Fatalf("unexpected CRD row %#v", row)
	}
}

func TestEnvtestDiscoveryListsBuiltinsAndCRDsWithin500ms(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	apiExt, err := apiextensionsclientset.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	crd := widgetCRD()
	if _, err := apiExt.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForCRDEstablished(ctx, t, apiExt, crd.Name)
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &cluster.Client{Discovery: cachedmemory.NewMemCacheClient(disco)}
	start := time.Now()
	resources, err := cluster.DiscoverResources(client)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("discovery took %s, exceeds 500ms", elapsed)
	}
	requireDiscovered(t, resources, schema.GroupVersionResource{Version: "v1", Resource: "pods"})
	requireDiscovered(t, resources, schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"})
}

func TestEnvtestBuiltinRenderersAgainstFixtureCluster(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	namespace := createNamespace(ctx, t, clientset, "astrotui-render")
	cache := cluster.NewResourceCache(dyn, 0)
	cache.Start(ctx)
	for _, fixture := range rendererFixtures(namespace) {
		t.Run(fixture.gvr.String(), func(t *testing.T) {
			if !cache.WaitForSync(ctx, fixture.gvr) {
				t.Fatalf("%s informer did not sync", fixture.gvr)
			}
			var resource dynamic.ResourceInterface
			if fixture.object.GetNamespace() != "" {
				resource = dyn.Resource(fixture.gvr).Namespace(fixture.object.GetNamespace())
			} else {
				resource = dyn.Resource(fixture.gvr)
			}
			created, err := resource.Create(ctx, fixture.object, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if status, ok, _ := unstructured.NestedMap(fixture.object.Object, "status"); ok {
				updated := created.DeepCopy()
				if err := unstructured.SetNestedMap(updated.Object, status, "status"); err != nil {
					t.Fatal(err)
				}
				if _, err := resource.UpdateStatus(ctx, updated, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			renderer := views.Builtins()[fixture.gvr]
			if renderer == nil {
				t.Fatalf("missing renderer for %s", fixture.gvr)
			}
			var rendered *unstructured.Unstructured
			var row []views.Cell
			if err := wait.PollUntilContextTimeout(ctx, 25*time.Millisecond, time.Second, true, func(context.Context) (bool, error) {
				var listErr error
				listed, listErr := cache.List(fixture.gvr, fixture.object.GetNamespace(), nil)
				for _, item := range listed {
					if item.GetName() == fixture.object.GetName() {
						rendered = item
						row = renderer.Row(rendered)
						for column, want := range fixture.want {
							if rendererCell(renderer, row, column) != want {
								return false, listErr
							}
						}
						return true, listErr
					}
				}
				return false, listErr
			}); err != nil {
				t.Fatal(err)
			}
			for column, want := range fixture.want {
				if got := rendererCell(renderer, row, column); got != want {
					t.Fatalf("%s column %s got %q want %q row=%#v", fixture.gvr, column, got, want, row)
				}
			}
		})
	}
}

func TestEnvtestRendererFixturesCoverEveryBuiltin(t *testing.T) {
	fixtures := rendererFixtures("default")
	covered := map[schema.GroupVersionResource]bool{}
	for _, fixture := range fixtures {
		covered[fixture.gvr] = true
	}
	var missing []string
	for gvr := range views.Builtins() {
		if !covered[gvr] {
			missing = append(missing, gvr.String())
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("renderer fixture coverage is missing built-ins: %v", missing)
	}
}

func startEnvtest(t *testing.T) (*rest.Config, func()) {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("set KUBEBUILDER_ASSETS or run `make envtest`")
	}
	env := &crenvtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, func() {
		if err := env.Stop(); err != nil {
			t.Fatalf("stop envtest: %v", err)
		}
	}
}

type rendererFixture struct {
	gvr    schema.GroupVersionResource
	object *unstructured.Unstructured
	want   map[string]string
}

func rendererFixtures(namespace string) []rendererFixture {
	return []rendererFixture{
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			object: fixtureObject("v1", "ConfigMap", "settings", namespace, map[string]any{
				"data": map[string]any{"mode": "test"},
			}),
			want: map[string]string{"NAME": "settings", "NAMESPACE": namespace, "DATA": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "secrets"},
			object: fixtureObject("v1", "Secret", "credentials", namespace, map[string]any{
				"type": "Opaque",
				"data": map[string]any{"sample": "dmFsdWU="},
			}),
			want: map[string]string{"NAME": "credentials", "TYPE": "Opaque", "DATA": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"},
			object: fixtureObject("v1", "ServiceAccount", "runner", namespace, map[string]any{
				"secrets": []any{map[string]any{"name": "runner-token"}},
			}),
			want: map[string]string{"NAME": "runner", "SECRET": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "pods"},
			object: fixtureObject("v1", "Pod", "api-pod", namespace, map[string]any{
				"spec": map[string]any{"containers": []any{map[string]any{
					"name":  "main",
					"image": "registry.k8s.io/pause:3.9",
				}}},
				"status": map[string]any{
					"phase": "Running",
					"containerStatuses": []any{map[string]any{
						"name":         "main",
						"ready":        true,
						"restartCount": int64(2),
					}},
				},
			}),
			want: map[string]string{"NAME": "api-pod", "READY": "1/1", "STATUS": "Running", "RESTARTS": "2"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"},
			object: fixtureObject("v1", "Endpoints", "api-endpoints", namespace, map[string]any{
				"subsets": []any{map[string]any{"addresses": []any{map[string]any{"ip": "10.0.0.1"}}}},
			}),
			want: map[string]string{"NAME": "api-endpoints", "ENDPOINTS": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "events"},
			object: fixtureObject("v1", "Event", "api.12345", namespace, map[string]any{
				"involvedObject": map[string]any{"kind": "Pod", "name": "api-pod", "namespace": namespace},
				"type":           "Warning",
				"reason":         "Failed",
				"message":        "pull failed",
				"source":         map[string]any{"component": "kubelet"},
				"firstTimestamp": "2026-05-09T00:00:00Z",
				"lastTimestamp":  "2026-05-09T00:00:00Z",
				"count":          int64(1),
			}),
			want: map[string]string{"TYPE": "Warning", "REASON": "Failed", "OBJECT": "Pod/api-pod", "MESSAGE": "pull failed"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
			object: fixtureObject("v1", "Namespace", namespace+"-extra", "", map[string]any{
				"status": map[string]any{"phase": "Active"},
			}),
			want: map[string]string{"NAME": namespace + "-extra", "STATUS": "Active"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "nodes"},
			object: fixtureObject("v1", "Node", "node-a", "", map[string]any{
				"metadata": map[string]any{
					"name":   "node-a",
					"labels": map[string]any{"node-role.kubernetes.io/worker": ""},
				},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
					"nodeInfo":   map[string]any{"kubeletVersion": "v1.30.0"},
				},
			}),
			want: map[string]string{"NAME": "node-a", "STATUS": "Ready", "ROLE": "worker", "VERSION": "v1.30.0"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "services"},
			object: fixtureObject("v1", "Service", "api", namespace, map[string]any{
				"spec": map[string]any{
					"type":     "ClusterIP",
					"selector": map[string]any{"app": "api"},
					"ports":    []any{map[string]any{"port": int64(80), "protocol": "TCP"}},
				},
			}),
			want: map[string]string{"NAME": "api", "TYPE": "ClusterIP", "PORTS": "80/TCP"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			object: fixtureObject("apps/v1", "Deployment", "api", namespace, map[string]any{
				"spec": map[string]any{
					"replicas": int64(3),
					"selector": map[string]any{"matchLabels": map[string]any{"app": "api"}},
					"template": map[string]any{
						"metadata": map[string]any{"labels": map[string]any{"app": "api"}},
						"spec": map[string]any{"containers": []any{map[string]any{
							"name":  "main",
							"image": "registry.k8s.io/pause:3.9",
						}}},
					},
				},
				"status": map[string]any{"readyReplicas": int64(2), "replicas": int64(3), "updatedReplicas": int64(3), "availableReplicas": int64(2)},
			}),
			want: map[string]string{"NAME": "api", "READY": "2/3", "UP-TO-DATE": "3", "AVAILABLE": "2"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"},
			object: fixtureObject("apps/v1", "DaemonSet", "agent", namespace, map[string]any{
				"spec": daemonSetSpec(),
				"status": map[string]any{
					"desiredNumberScheduled": int64(2),
					"currentNumberScheduled": int64(2),
					"numberReady":            int64(1),
					"updatedNumberScheduled": int64(2),
					"numberAvailable":        int64(1),
				},
			}),
			want: map[string]string{"NAME": "agent", "DESIRED": "2", "CURRENT": "2", "READY": "1/2"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"},
			object: fixtureObject("apps/v1", "StatefulSet", "db", namespace, map[string]any{
				"spec": mergeMaps(workloadSpec(2), map[string]any{"serviceName": "db"}),
				"status": map[string]any{
					"replicas":      int64(2),
					"readyReplicas": int64(1),
				},
			}),
			want: map[string]string{"NAME": "db", "READY": "1/2", "SERVICE": "db"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"},
			object: fixtureObject("apps/v1", "ReplicaSet", "api-rs", namespace, map[string]any{
				"spec": workloadSpec(2),
				"status": map[string]any{
					"replicas":      int64(2),
					"readyReplicas": int64(1),
				},
			}),
			want: map[string]string{"NAME": "api-rs", "DESIRED": "2", "CURRENT": "2", "READY": "1/2"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"},
			object: fixtureObject("batch/v1", "Job", "import", namespace, map[string]any{
				"spec": map[string]any{
					"completions": int64(1),
					"template":    podTemplateWithRestart("Never"),
				},
				"status": map[string]any{
					"succeeded": int64(1),
				},
			}),
			want: map[string]string{"NAME": "import", "COMPLETIONS": "1/1"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"},
			object: fixtureObject("batch/v1", "CronJob", "sync", namespace, map[string]any{
				"spec": map[string]any{
					"schedule": "*/5 * * * *",
					"suspend":  false,
					"jobTemplate": map[string]any{"spec": map[string]any{
						"template": podTemplateWithRestart("Never"),
					}},
				},
				"status": map[string]any{
					"active":           []any{map[string]any{"name": "sync-1"}},
					"lastScheduleTime": "2026-05-09T00:00:00Z",
				},
			}),
			want: map[string]string{"NAME": "sync", "SCHEDULE": "*/5 * * * *", "ACTIVE": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
			object: fixtureObject("autoscaling/v2", "HorizontalPodAutoscaler", "api-hpa", namespace, map[string]any{
				"spec": map[string]any{
					"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "api"},
					"minReplicas":    int64(1),
					"maxReplicas":    int64(5),
				},
				"status": map[string]any{"currentReplicas": int64(2), "desiredReplicas": int64(3)},
			}),
			want: map[string]string{"NAME": "api-hpa", "REFERENCE": "Deployment/api", "TARGETS": "2/3", "MAXPODS": "5"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
			object: fixtureObject("policy/v1", "PodDisruptionBudget", "api-pdb", namespace, map[string]any{
				"spec": map[string]any{"minAvailable": int64(1), "selector": map[string]any{"matchLabels": map[string]any{"app": "api"}}},
				"status": map[string]any{
					"currentHealthy":     int64(2),
					"desiredHealthy":     int64(1),
					"expectedPods":       int64(3),
					"disruptionsAllowed": int64(1),
				},
			}),
			want: map[string]string{"NAME": "api-pdb", "MIN-AVAILABLE": "1", "ALLOWED-DISRUPTIONS": "1", "EXPECTED": "3"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
			object: fixtureObject("networking.k8s.io/v1", "NetworkPolicy", "api", namespace, map[string]any{
				"spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "api"}}},
			}),
			want: map[string]string{"NAME": "api", "POD-SELECTOR": "app=api"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
			object: fixtureObject("networking.k8s.io/v1", "Ingress", "web", namespace, map[string]any{
				"spec": map[string]any{
					"ingressClassName": "nginx",
					"rules":            []any{map[string]any{"host": "app.example.com"}},
				},
				"status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": "1.2.3.4"}}}},
			}),
			want: map[string]string{"NAME": "web", "CLASS": "nginx", "HOSTS": "app.example.com", "ADDRESS": "1.2.3.4"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"},
			object: fixtureObject("discovery.k8s.io/v1", "EndpointSlice", "api-slice", namespace, map[string]any{
				"addressType": "IPv4",
				"ports":       []any{map[string]any{"port": int64(80), "protocol": "TCP"}},
				"endpoints":   []any{map[string]any{"addresses": []any{"10.0.0.1"}}},
			}),
			want: map[string]string{"NAME": "api-slice", "ADDRESSTYPE": "IPv4", "PORTS": "80/TCP", "ENDPOINTS": "1"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"},
			object: fixtureObject("v1", "PersistentVolume", "pv-a", "", map[string]any{
				"spec": map[string]any{
					"capacity":                      map[string]any{"storage": "1Gi"},
					"accessModes":                   []any{"ReadWriteOnce"},
					"persistentVolumeReclaimPolicy": "Delete",
					"storageClassName":              "fast",
					"hostPath":                      map[string]any{"path": "/tmp/data"},
				},
				"status": map[string]any{"phase": "Available"},
			}),
			want: map[string]string{"NAME": "pv-a", "CAPACITY": "1Gi", "ACCESS MODES": "ReadWriteOnce", "STATUS": "Available"},
		},
		{
			gvr: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"},
			object: fixtureObject("v1", "PersistentVolumeClaim", "data", namespace, map[string]any{
				"spec": map[string]any{
					"accessModes": []any{"ReadWriteOnce"},
					"resources":   map[string]any{"requests": map[string]any{"storage": "1Gi"}},
				},
				"status": map[string]any{"phase": "Pending"},
			}),
			want: map[string]string{"NAME": "data", "STATUS": "Pending", "ACCESS MODES": "ReadWriteOnce"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"},
			object: fixtureObject("storage.k8s.io/v1", "StorageClass", "fast", "", map[string]any{
				"provisioner":          "kubernetes.io/no-provisioner",
				"reclaimPolicy":        "Delete",
				"volumeBindingMode":    "WaitForFirstConsumer",
				"allowVolumeExpansion": true,
			}),
			want: map[string]string{"NAME": "fast", "PROVISIONER": "kubernetes.io/no-provisioner", "RECLAIMPOLICY": "Delete"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "volumeattachments"},
			object: fixtureObject("storage.k8s.io/v1", "VolumeAttachment", "attach-a", "", map[string]any{
				"spec": map[string]any{
					"attacher": "csi.example.com",
					"nodeName": "node-a",
					"source":   map[string]any{"persistentVolumeName": "pv-a"},
				},
				"status": map[string]any{"attached": true},
			}),
			want: map[string]string{"NAME": "attach-a", "ATTACHER": "csi.example.com", "PV": "pv-a", "NODE": "node-a", "ATTACHED": "true"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
			object: fixtureObject("rbac.authorization.k8s.io/v1", "Role", "reader", namespace, map[string]any{
				"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}},
			}),
			want: map[string]string{"NAME": "reader", "NAMESPACE": namespace},
		},
		{
			gvr: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
			object: fixtureObject("rbac.authorization.k8s.io/v1", "RoleBinding", "readers", namespace, map[string]any{
				"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "reader"},
				"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "default", "namespace": namespace}},
			}),
			want: map[string]string{"NAME": "readers", "ROLE": "Role/reader", "KIND": "Role", "SUBJECTS": "default"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
			object: fixtureObject("rbac.authorization.k8s.io/v1", "ClusterRole", "cluster-reader", "", map[string]any{
				"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}},
			}),
			want: map[string]string{"NAME": "cluster-reader"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
			object: fixtureObject("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "cluster-readers", "", map[string]any{
				"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "cluster-reader"},
				"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "default", "namespace": namespace}},
			}),
			want: map[string]string{"NAME": "cluster-readers", "CLUSTERROLE": "cluster-reader", "SUBJECT-KIND": "ServiceAccount", "SUBJECTS": "default"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"},
			object: fixtureObject("coordination.k8s.io/v1", "Lease", "leader", namespace, map[string]any{
				"spec": map[string]any{"holderIdentity": "api-0"},
			}),
			want: map[string]string{"NAME": "leader", "HOLDER": "api-0"},
		},
		{
			gvr: schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"},
			object: fixtureObject("apiregistration.k8s.io/v1", "APIService", "v1alpha1.widgets.example.com", "", map[string]any{
				"spec": map[string]any{
					"group":                "widgets.example.com",
					"version":              "v1alpha1",
					"groupPriorityMinimum": int64(1000),
					"versionPriority":      int64(15),
				},
				"status": map[string]any{"conditions": []any{map[string]any{"type": "Available", "status": "True"}}},
			}),
			want: map[string]string{"NAME": "v1alpha1.widgets.example.com", "AVAILABLE": "True"},
		},
	}
}

func fixtureObject(apiVersion, kind, name, namespace string, fields map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]any{
				"app": "api",
			},
		},
	}}
	if namespace != "" {
		obj.Object["metadata"].(map[string]any)["namespace"] = namespace
	}
	for key, value := range fields {
		obj.Object[key] = value
	}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	return obj
}

func workloadSpec(replicas int64) map[string]any {
	return map[string]any{
		"replicas": replicas,
		"selector": map[string]any{"matchLabels": map[string]any{
			"app": "api",
		}},
		"template": podTemplate(),
	}
}

func daemonSetSpec() map[string]any {
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{
			"app": "api",
		}},
		"template": podTemplate(),
	}
}

func podTemplate() map[string]any {
	return map[string]any{
		"metadata": map[string]any{"labels": map[string]any{
			"app": "api",
		}},
		"spec": map[string]any{"containers": []any{map[string]any{
			"name":  "main",
			"image": "registry.k8s.io/pause:3.9",
		}}},
	}
}

func podTemplateWithRestart(policy string) map[string]any {
	template := podTemplate()
	spec := template["spec"].(map[string]any)
	spec["restartPolicy"] = policy
	return template
}

func mergeMaps(base map[string]any, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func rendererCell(renderer views.Renderer, row []views.Cell, column string) string {
	for i, header := range renderer.Header() {
		if header.Name == column && i < len(row) {
			return row[i].Text
		}
	}
	return ""
}

func requireDiscovered(t *testing.T, resources []schema.GroupVersionResource, want schema.GroupVersionResource) {
	t.Helper()
	for _, got := range resources {
		if got == want {
			return
		}
	}
	t.Fatalf("expected discovery result %s in %#v", want, resources)
}

func createNamespace(ctx context.Context, t *testing.T, client kubernetes.Interface, prefix string) string {
	t.Helper()
	ns := prefix + "-" + time.Now().Format("150405000000")
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return ns
}

func widgetCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "widgets",
				Singular: "widget",
				Kind:     "Widget",
				ListKind: "WidgetList",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"size": {Type: "string"},
						}},
					},
				}},
				AdditionalPrinterColumns: []apiextensionsv1.CustomResourceColumnDefinition{{
					Name:     "Size",
					Type:     "string",
					JSONPath: ".spec.size",
				}},
			}},
		},
	}
}

func waitForCRDEstablished(ctx context.Context, t *testing.T, client apiextensionsclientset.Interface, name string) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
}
