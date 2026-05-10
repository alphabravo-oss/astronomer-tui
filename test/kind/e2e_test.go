//go:build e2e

package kind

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/ui"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

func TestActionsGoldenPath(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, cluster.ListOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces.Items) == 0 {
		t.Fatal("expected at least one namespace")
	}
	namespace := fmt.Sprintf("astrotui-e2e-%d", time.Now().UnixNano())
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	}()
	configMapGVR := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	configMap := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": namespace,
		},
		"data": map[string]any{"mode": "test"},
	}}
	if _, err := client.Dynamic.Resource(configMapGVR).Namespace(namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	guardedConfigMap := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "guarded",
			"namespace": namespace,
		},
		"data": map[string]any{"mode": "guarded"},
	}}
	if _, err := client.Dynamic.Resource(configMapGVR).Namespace(namespace).Create(ctx, guardedConfigMap, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "pause",
					Image: "registry.k8s.io/pause:3.9",
				}}},
			},
		},
	}
	if _, err := client.Clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	executor := actions.Executor{
		Clientset:      client.Clientset,
		Dynamic:        client.Dynamic,
		Config:         config.Default(),
		CurrentContext: client.Context,
	}
	if _, err := executor.Run(ctx, actions.Request{Name: "scale", Kind: "Deployment", Namespace: namespace, ResourceName: "api", Args: map[string]string{"replicas": "2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Run(ctx, actions.Request{Name: "restart", Kind: "Deployment", Namespace: namespace, ResourceName: "api"}); err != nil {
		t.Fatal(err)
	}
	current, err := client.Dynamic.Resource(configMapGVR).Namespace(namespace).Get(ctx, "settings", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actions.EditResource(ctx, client.Dynamic, configMapGVR, current, editFunc(func(path string) error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(path, bytes.ReplaceAll(data, []byte("mode: test"), []byte("mode: edited")), 0o600)
	})); err != nil {
		t.Fatal(err)
	}
	edited, err := client.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, "settings", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Data["mode"] != "edited" {
		t.Fatalf("expected edited configmap data, got %#v", edited.Data)
	}
	prodExecutor := executor
	prodExecutor.CurrentContext = "kind-prod"
	if _, err := prodExecutor.Run(ctx, actions.Request{Name: "delete", Kind: "ConfigMap", Namespace: namespace, ResourceName: "guarded", GVR: configMapGVR}); err == nil {
		t.Fatal("expected prod guard to reject delete without typed confirmation")
	}
	if _, err := prodExecutor.Run(ctx, actions.Request{Name: "delete", Kind: "ConfigMap", Namespace: namespace, ResourceName: "guarded", GVR: configMapGVR, ConfirmText: "kind-prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Run(ctx, actions.Request{Name: "delete", Kind: "ConfigMap", Namespace: namespace, ResourceName: "settings", GVR: configMapGVR}); err != nil {
		t.Fatal(err)
	}
}

func TestKubeconfigGoldenPath(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, cluster.ListOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces.Items) == 0 {
		t.Fatal("expected kind kubeconfig to list at least one namespace")
	}
}

func TestLogsGoldenPath(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespace := fmt.Sprintf("astrotui-logs-%d", time.Now().UnixNano())
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	}()
	const logLine = "astrotui-log-golden-line"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "logger", Namespace: namespace, Labels: map[string]string{"app": "logger"}},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "main",
				Image:   "registry.k8s.io/e2e-test-images/busybox:1.29-4",
				Command: []string{"sh", "-c", "echo " + logLine},
			}},
		},
	}
	if _, err := client.Clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		got, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, "logger", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return got.Status.Phase == corev1.PodSucceeded || got.Status.Phase == corev1.PodFailed, nil
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	target := logs.PodLogTarget{Namespace: namespace, Pod: "logger", Container: "main"}
	if err := logs.StreamPodLogs(ctx, client.Clientset, target, logs.RequestOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), logLine) {
		t.Fatalf("streamed logs missing %q:\n%s", logLine, buf.String())
	}
}

func TestExecAndPortForwardGoldenPath(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespace := fmt.Sprintf("astrotui-io-%d", time.Now().UnixNano())
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	}()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "shell", Namespace: namespace, Labels: map[string]string{"app": "shell"}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "main",
				Image:   "registry.k8s.io/e2e-test-images/busybox:1.29-4",
				Command: []string{"sh", "-c", "sleep 300"},
				Ports:   []corev1.ContainerPort{{ContainerPort: 8080}},
			}},
		},
	}
	if _, err := client.Clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForPodPhase(ctx, t, client, namespace, "shell", corev1.PodRunning)
	var stdout, stderr bytes.Buffer
	target := actions.ExecTarget{Namespace: namespace, Pod: "shell", Container: "main", Command: []string{"sh", "-c", "echo astrotui-exec-golden"}}
	if err := actions.RunExec(ctx, client.Config, client.Clientset, target, nil, &stdout, &stderr); err != nil {
		t.Fatalf("exec failed: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "astrotui-exec-golden") {
		t.Fatalf("unexpected exec stdout %q stderr=%q", stdout.String(), stderr.String())
	}
	localPort := freeLocalPort(t)
	forwardCtx, forwardCancel := context.WithCancel(ctx)
	defer forwardCancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- actions.RunPortForward(forwardCtx, client.Config, client.Clientset, actions.PortForwardSpec{
			Namespace: namespace,
			Resource:  "pod/shell",
			Local:     localPort,
			Remote:    8080,
		}, &bytes.Buffer{}, &bytes.Buffer{})
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("port-forward exited before cancellation: %v", err)
		}
		t.Fatal("port-forward exited before cancellation")
	case <-time.After(1500 * time.Millisecond):
	}
	forwardCancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("port-forward did not stop after cancellation")
	}
}

func TestKindPerformanceGoldenPath(t *testing.T) {
	requireKindE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespace := fmt.Sprintf("astrotui-perf-%d", time.Now().UnixNano())
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	}()
	createPerfPods(ctx, t, client.Clientset, namespace, 1_000)
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	coldStart := time.Now()
	cache := cluster.NewResourceCache(client.Dynamic, 0)
	cache.Start(ctx)
	if !cache.WaitForSync(ctx, gvr) {
		t.Fatal("pod informer did not sync")
	}
	renderer := views.Builtins()[gvr]
	start := time.Now()
	listed, err := cache.List(gvr, namespace, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, visible := ui.BuildRows(renderer, listed, "", 0, false)
	elapsed := time.Since(start)
	if len(rows) != 1_000 || len(visible) != 1_000 {
		t.Fatalf("expected 1000 rendered pods, got rows=%d visible=%d", len(rows), len(visible))
	}
	if elapsed := time.Since(coldStart); elapsed > 300*time.Millisecond {
		t.Fatalf("kind cache sync plus first render took %s, exceeds 300ms", elapsed)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("kind cache-to-render pass took %s, exceeds 100ms", elapsed)
	}
}

func TestHeadlessRenderProcessGoldenPath(t *testing.T) {
	requireKindE2E(t)
	idle := processNFRIdleDuration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second+idle)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT")})
	if err != nil {
		t.Fatal(err)
	}
	namespace := fmt.Sprintf("astrotui-process-%d", time.Now().UnixNano())
	if _, err := client.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.Clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	}()
	createPerfPods(ctx, t, client.Clientset, namespace, 1_000)
	binary := buildPerfBinary(t)
	cmd := exec.CommandContext(ctx, binary,
		"--context", os.Getenv("ASTROTUI_E2E_CONTEXT"),
		"--namespace", namespace,
		"--headless-render",
		"--headless-render-min-rows", "1000",
		"--headless-render-idle", idle.String(),
	) // #nosec G204 -- e2e executes the freshly built local binary with fixed arguments.
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("headless render failed: %v\n%s", err, output)
	}
	text := string(output)
	renderMS := metricFromOutput(t, text, "render_ms")
	totalMS := metricFromOutput(t, text, "total_ms")
	if renderMS > 100 {
		t.Fatalf("headless render took %dms, exceeds 100ms:\n%s", renderMS, text)
	}
	if totalMS > 300 {
		t.Fatalf("headless process-to-render took %dms, exceeds 300ms:\n%s", totalMS, text)
	}
	maxIdleCPU := idle.Milliseconds() / 50
	if idleCPU := metricFromOutput(t, text, "idle_cpu_ms"); idleCPU > maxIdleCPU {
		t.Fatalf("headless idle CPU took %dms over %s, exceeds 2%%:\n%s", idleCPU, idle, text)
	}
	if rss := metricFromOutput(t, text, "rss_bytes"); rss > 150*1024*1024 {
		t.Fatalf("headless RSS got %d bytes, exceeds 150MiB:\n%s", rss, text)
	}
}

func processNFRIdleDuration(t *testing.T) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("ASTROTUI_E2E_PROCESS_IDLE"))
	if value == "" {
		return 2 * time.Second
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("parse ASTROTUI_E2E_PROCESS_IDLE=%q: %v", value, err)
	}
	return duration
}

type editFunc func(string) error

func (f editFunc) Edit(path string) error {
	return f(path)
}

func waitForPodPhase(ctx context.Context, t *testing.T, client *cluster.Client, namespace, name string, phase corev1.PodPhase) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 45*time.Second, true, func(ctx context.Context) (bool, error) {
		pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return pod.Status.Phase == phase, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func metricFromOutput(t *testing.T, output, key string) int64 {
	t.Helper()
	prefix := key + "="
	for _, field := range strings.Fields(output) {
		if value, ok := strings.CutPrefix(field, prefix); ok {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Fatalf("parse %s from %q: %v", key, field, err)
			}
			return parsed
		}
	}
	t.Fatalf("missing %s in output %q", key, output)
	return 0
}

func createPerfPods(ctx context.Context, t *testing.T, clientset kubernetes.Interface, namespace string, count int) {
	t.Helper()
	const workers = 25
	jobs := make(chan int)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				name := fmt.Sprintf("api-%04d", i)
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"app": "perf"}},
					Spec: corev1.PodSpec{
						NodeSelector:  map[string]string{"astrotui.dev/nonexistent": "true"},
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{{
							Name:  "pause",
							Image: "registry.k8s.io/pause:3.9",
						}},
					},
				}
				if _, err := clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
					errs <- fmt.Errorf("create pod %s: %w", name, err)
					return
				}
			}
		}()
	}
	for i := range count {
		select {
		case jobs <- i:
		case err := <-errs:
			close(jobs)
			wg.Wait()
			t.Fatal(err)
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			t.Fatal(ctx.Err())
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func requireKindE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("ASTROTUI_E2E") != "1" {
		t.Skip("set ASTROTUI_E2E=1 and ASTROTUI_E2E_CONTEXT=<kind-context> to run kind e2e")
	}
}
