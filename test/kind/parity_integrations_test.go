//go:build e2e

package kind

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/integrations"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLiveHelmLifecycle(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ns := fmt.Sprintf("astrotui-helm-%d", time.Now().UnixNano())
	c, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT"), Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = c.Clientset.CoreV1().Namespaces().Delete(clean, ns, metav1.DeleteOptions{})
	}()
	dir := t.TempDir()
	if err = os.Mkdir(filepath.Join(dir, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Chart.yaml": "apiVersion: v2\nname: fixture\nversion: 0.1.0\n", "values.yaml": "mode: one\n", "templates/config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: helm-fixture\ndata:\n  mode: {{ .Values.mode | quote }}\n"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = integrations.Helm(ctx, c, ns, "install", "fixture", dir, "--wait"); err != nil {
		t.Fatal(err)
	}
	releases, err := integrations.ListReleases(ctx, c, ns)
	if err != nil || len(releases) != 1 {
		t.Fatalf("%+v %v", releases, err)
	}
	original := releases[0]
	if _, err = integrations.Helm(ctx, c, ns, "upgrade", "fixture", dir, "--set", "mode=two", "--wait"); err != nil {
		t.Fatal(err)
	}
	if err = integrations.CheckRevision(ctx, c, original); err == nil {
		t.Fatal("stale revision accepted")
	}
	history, err := integrations.History(ctx, c, original)
	if err != nil || len(history) != 2 {
		t.Fatalf("history %+v %v", history, err)
	}
	if _, err = integrations.Helm(ctx, c, ns, "rollback", "fixture", "1", "--wait"); err != nil {
		t.Fatal(err)
	}
	cm, err := c.Clientset.CoreV1().ConfigMaps(ns).Get(ctx, "helm-fixture", metav1.GetOptions{})
	if err != nil || cm.Data["mode"] != "one" {
		t.Fatalf("rollback did not restore value: %v", err)
	}
	if _, err = integrations.Helm(ctx, c, ns, "get", "manifest", "fixture", "--revision", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = integrations.Helm(ctx, c, ns, "uninstall", "fixture", "--wait"); err != nil {
		t.Fatal(err)
	}
}
func TestLiveNodeShellCleanup(t *testing.T) {
	requireKindE2E(t)
	if os.Getenv("ASTROTUI_NODE_SHELL_E2E") != "1" {
		t.Skip("explicit privileged fixture opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := c.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) != 1 {
		t.Fatal("test requires isolated single-node kind cluster", err)
	}
	node := nodes.Items[0]
	if !strings.HasPrefix(node.Name, "astrotui-") || !strings.HasPrefix(c.Context, "kind-astrotui-") {
		t.Fatal("unexpected node fixture")
	}
	target, cleanup, err := actions.PrepareNodeShell(ctx, c.Clientset, node.Name, node.UID, "default", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	target.Command[len(target.Command)-1] = "true"
	var out, errOut bytes.Buffer
	if err = actions.RunExec(ctx, c.Config, c.Clientset, target, nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Clientset.CoreV1().Pods(target.Namespace).Get(ctx, target.Pod, metav1.GetOptions{}); err == nil {
		t.Fatal("helper pod remains")
	}
}
