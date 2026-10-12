package envtest

import (
	"context"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	cachedmemory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"testing"
	"time"
)

func TestNamespaceRestrictedInformerAgainstAPIServer(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ns := createNamespace(ctx, t, admin, "restricted")
	_, err = admin.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: ns}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get", "list", "watch"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: ns}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "restricted-reader", APIGroup: rbacv1.GroupName}}, RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "reader", APIGroup: rbacv1.GroupName}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "visible", Namespace: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	restricted := rest.CopyConfig(cfg)
	restricted.Impersonate = rest.ImpersonationConfig{UserName: "restricted-reader"}
	dyn, err := dynamic.NewForConfig(restricted)
	if err != nil {
		t.Fatal(err)
	}
	g := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	if err = wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		_, e := dyn.Resource(g).Namespace(ns).List(ctx, metav1.ListOptions{})
		return e == nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = dyn.Resource(g).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("fixture must prohibit cluster LIST: %v", err)
	}
	cache := cluster.NewScopedResourceCache(dyn, 0, ns)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, g) {
		t.Fatal("restricted informer did not sync")
	}
	objects, err := cache.List(g, ns, nil)
	if err != nil || len(objects) != 1 {
		t.Fatalf("objects=%d err=%v", len(objects), err)
	}
}
func TestDiscoveryInvalidationAndServerPrinterColumns(t *testing.T) {
	cfg, stop := startEnvtest(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ext, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	crd := widgetCRD()
	disco, _ := discovery.NewDiscoveryClientForConfig(cfg)
	cached := cachedmemory.NewMemCacheClient(disco)
	before, err := cluster.LoadCatalog(cached)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = before.Resolve("Widget"); err == nil {
		t.Fatal("unexpected pre-existing CRD")
	}
	if _, err = ext.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForCRDEstablished(ctx, t, ext, crd.Name)
	cached.Invalidate()
	catalog, err := cluster.LoadCatalog(cached)
	if err != nil {
		t.Fatal(err)
	}
	g, err := catalog.Resolve("Widget")
	if err != nil {
		t.Fatal(err)
	}
	client, _ := kubernetes.NewForConfig(cfg)
	ns := createNamespace(ctx, t, client, "printer")
	dyn, _ := dynamic.NewForConfig(cfg)
	widget := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.com/v1", "kind": "Widget", "metadata": map[string]any{"name": "printed", "namespace": ns}, "spec": map[string]any{"size": "large"}}}
	created, err := dyn.Resource(g).Namespace(ns).Create(ctx, widget, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	table, err := cluster.FetchTable(ctx, cfg, g, ns)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Rows) != 1 || len(table.Rows[0].Cells) < 2 || table.Rows[0].Cells[1] != "large" {
		t.Fatalf("server printer: %#v", table)
	}
	// Real API validates optimistic identity on administrative patches.
	executor := actions.Executor{Dynamic: dyn, Clientset: client}
	req := actions.Request{Name: "suspend", Kind: "Job", GVR: g, Namespace: ns, ResourceName: created.GetName(), UID: "replacement"}
	if _, err = executor.Run(ctx, req); err == nil {
		t.Fatal("identity precondition ignored")
	}
	if err = ext.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, crd.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		cached.Invalidate()
		c, _ := cluster.LoadCatalog(cached)
		_, e := c.Resolve("Widget")
		return e != nil, nil
	}); err != nil {
		t.Fatal("deleted CRD remained in refreshed catalog")
	}
}
