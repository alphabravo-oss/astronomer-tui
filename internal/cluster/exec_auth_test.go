package cluster

import (
	"context"
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExecCredentialAuthentication(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "credential")
	data := "#!/bin/sh\nprintf '%s' '{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"kind\":\"ExecCredential\",\"status\":{\"token\":\"fixture-token\"}}'\n"
	if err := os.WriteFile(script, []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("exec credential not attached")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "NamespaceList", "items": []any{}})
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, ExecProvider: &clientcmdapi.ExecConfig{Command: script, APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestExecCredentialRefreshAfterExpiry(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "credential")
	data := `#!/bin/sh
if [ -f "$ASTROTUI_AUTH_MARKER" ]; then
 printf '%s' '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"refreshed"}}'
else
 touch "$ASTROTUI_AUTH_MARKER"
 printf '%s' '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"expired","expirationTimestamp":"2000-01-01T00:00:00Z"}}'
fi
`
	if err := os.WriteFile(script, []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
	var tokens []string
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "NamespaceList", "items": []any{}})
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, ExecProvider: &clientcmdapi.ExecConfig{Command: script, APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode, Env: []clientcmdapi.ExecEnvVar{{Name: "ASTROTUI_AUTH_MARKER", Value: filepath.Join(dir, "invoked")}}}}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = client.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 || tokens[0] != "Bearer expired" || tokens[1] != "Bearer refreshed" {
		t.Fatalf("refresh sequence: %v", tokens)
	}
}
