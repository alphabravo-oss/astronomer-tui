package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/cached/memory"
	discoveryfake "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

func TestListNamespacesSorted(t *testing.T) {
	client := &Client{Clientset: kubefake.NewSimpleClientset(
		namespace("zeta"),
		namespace("default"),
	)}
	got, err := ListNamespaces(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "default,zeta" {
		t.Fatalf("unexpected namespaces %#v", got)
	}
	got, err = ListNamespaces(context.Background(), &Client{})
	if err != nil || got != nil {
		t.Fatalf("expected nil namespaces for missing clientset, got %#v %v", got, err)
	}
}

func TestDiscoverResourcesFiltersSubresources(t *testing.T) {
	disco := &discoveryfake.FakeDiscovery{Fake: &ktesting.Fake{}}
	disco.Resources = []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", Verbs: []string{"list"}},
			{Name: "pods/log", Verbs: []string{"get"}},
			{Name: "ignored"},
		},
	}}
	resources, err := DiscoverResources(&Client{Discovery: memory.NewMemCacheClient(disco)})
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Resource != "pods" {
		t.Fatalf("unexpected resources %#v", resources)
	}
}

func TestClientConfigHelpers(t *testing.T) {
	t.Setenv("KUBECONFIG", "/tmp/kubeconfig")
	if got := kubeconfigPath(clientcmd.NewDefaultClientConfigLoadingRules()); got != "/tmp/kubeconfig" {
		t.Fatalf("unexpected kubeconfig path %q", got)
	}
	err := plainConfigError("/tmp/kubeconfig", errors.New("boom"))
	if !strings.Contains(err.Error(), "/tmp/kubeconfig") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error %v", err)
	}
	if !containsSlash("pods/log") || containsSlash("pods") {
		t.Fatal("unexpected containsSlash behavior")
	}
}

func TestNewClientLoadsKubeconfigWithoutNetwork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	data := []byte(`
apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:65535
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: test-token
contexts:
- name: test
  context:
    cluster: test
    user: test
    namespace: kube-system
- name: other
  context:
    cluster: test
    user: test
    namespace: other-system
current-context: test
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	client, err := NewClient(context.Background(), ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Context != "test" || client.Namespace != "kube-system" || client.RESTConfigQPS != 50 || client.RESTConfigBurst != 100 {
		t.Fatalf("unexpected client %#v", client)
	}
	client, err = NewClient(context.Background(), ClientOptions{Context: "test", Namespace: "override"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Namespace != "override" {
		t.Fatalf("expected namespace override, got %q", client.Namespace)
	}
	client, err = NewClient(context.Background(), ClientOptions{Context: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Context != "other" || client.Namespace != "other-system" {
		t.Fatalf("expected context override, got context=%q namespace=%q", client.Context, client.Namespace)
	}
	client, err = NewClient(context.Background(), ClientOptions{
		Context: "test", AllNamespaces: true, ImpersonateUser: "alice", ImpersonateUID: "1001", ImpersonateGroups: []string{"operators"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Namespace != "" || client.Config.Impersonate.UserName != "alice" || client.Config.Impersonate.UID != "1001" || len(client.Config.Impersonate.Groups) != 1 {
		t.Fatalf("unexpected all-namespace impersonated client %#v", client)
	}
}

func TestExecAuthPluginRegistrationImportPresent(t *testing.T) {
	data, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `client-go/plugin/pkg/client/auth/exec`) {
		t.Fatal("client.go must keep the exec auth plugin registration import")
	}
}

func TestNewClientExecAuthHelperProvidesBearerToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer exec-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"default"}}]}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	helper := filepath.Join(dir, "fake-exec-auth")
	if err := os.WriteFile(helper, []byte(`#!/bin/sh
printf '{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","status":{"token":"exec-token"}}'
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o700); err != nil { // #nosec G302 -- test helper must be executable for client-go exec auth.
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config")
	data := fmt.Sprintf(`
apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: exec-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: %s
contexts:
- name: test
  context:
    cluster: test
    user: exec-user
    namespace: default
current-context: test
`, server.URL, helper)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	client, err := NewClient(context.Background(), ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	namespaces, err := client.Clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces.Items) != 1 || namespaces.Items[0].Name != "default" {
		t.Fatalf("unexpected namespaces %#v", namespaces.Items)
	}
}

func TestNewClientRejectsEmptyCurrentContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	_, err := NewClient(context.Background(), ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "current-context is empty") {
		t.Fatalf("expected empty context error, got %v", err)
	}
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}
