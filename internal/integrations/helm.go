package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
)

type limitedOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := b.limit - b.data.Len()
	if len(p) > left {
		b.overflow = true
		p = p[:left]
	}
	_, _ = b.data.Write(p)
	return n, nil
}

// Tool runs an optional native integration with bounded output and cancellation.
func Tool(ctx context.Context, binary string, args []string, env []string) ([]byte, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%s executable is required for this integration: %w", binary, err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), env...)
	out := &limitedOutput{limit: 16 * 1024 * 1024}
	stderr := &limitedOutput{limit: 64 * 1024}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", binary, err, stderr.data.String())
	}
	if out.overflow || stderr.overflow {
		return nil, fmt.Errorf("%s output exceeded the display limit", binary)
	}
	return out.data.Bytes(), nil
}

// Helm uses a private snapshot of the captured kubeconfig, never the ambient
// current-context. It removes the temporary credentials after each invocation.
func Helm(ctx context.Context, client *cluster.Client, namespace string, args ...string) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client unavailable")
	}
	raw := client.RawConfig.DeepCopy()
	raw.CurrentContext = client.Context
	data, err := clientcmd.Write(*raw)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "astrotui-helm-*.kubeconfig")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	flags := []string{"--kubeconfig", f.Name(), "--kube-context", client.Context}
	if namespace != "" {
		flags = append(flags, "--namespace", namespace)
	}
	return Tool(ctx, "helm", append(flags, args...), nil)
}

type Release struct {
	Name, Namespace, Revision, Status, Chart, AppVersion string
	UID                                                  types.UID `json:"-"`
}
type Revision struct {
	Revision                   int `json:"revision"`
	Status, Chart, Description string
}

func ListReleases(ctx context.Context, c *cluster.Client, ns string) ([]Release, error) {
	args := []string{"list", "--all", "--max", "1000", "--output", "json"}
	if ns == "" {
		args = append(args, "--all-namespaces")
	}
	data, err := Helm(ctx, c, ns, args...)
	if err != nil {
		return nil, err
	}
	var out []Release
	err = json.Unmarshal(data, &out)
	if err != nil {
		return nil, err
	}
	for i := range out {
		uid, err := releaseUID(ctx, c, out[i])
		if err != nil {
			return nil, err
		}
		out[i].UID = uid
	}
	return out, nil
}
func History(ctx context.Context, c *cluster.Client, r Release) ([]Revision, error) {
	data, err := Helm(ctx, c, r.Namespace, "history", r.Name, "--max", "1000", "--output", "json")
	if err != nil {
		return nil, err
	}
	var out []Revision
	err = json.Unmarshal(data, &out)
	return out, err
}
func CheckRevision(ctx context.Context, c *cluster.Client, r Release) error {
	history, err := History(ctx, c, r)
	if err != nil {
		return err
	}
	latest := 0
	for _, h := range history {
		if h.Revision > latest {
			latest = h.Revision
		}
	}
	uid, err := releaseUID(ctx, c, r)
	if err != nil {
		return err
	}
	if r.UID == "" || uid != r.UID {
		return fmt.Errorf("release was replaced; refresh before mutating")
	}
	expected, err := strconv.Atoi(r.Revision)
	if err != nil || latest != expected {
		return fmt.Errorf("release revision changed; refresh before mutating")
	}
	return nil
}

func releaseUID(ctx context.Context, c *cluster.Client, r Release) (types.UID, error) {
	name := "sh.helm.release.v1." + r.Name + ".v" + r.Revision
	switch os.Getenv("HELM_DRIVER") {
	case "", "secret", "secrets":
		s, err := c.Clientset.CoreV1().Secrets(r.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return s.UID, nil
	case "configmap", "configmaps":
		s, err := c.Clientset.CoreV1().ConfigMaps(r.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return s.UID, nil
	default:
		return "", fmt.Errorf("Helm browser requires Secret or ConfigMap storage for identity checks")
	}
}
