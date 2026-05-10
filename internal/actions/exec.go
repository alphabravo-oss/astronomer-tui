//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var ShellFallback = []string{"bash", "sh", "/bin/ash"}

type ExecTarget struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
}

func ExecTargets(pod corev1.Pod, preferredContainer string) ([]ExecTarget, error) {
	container, ok := pickContainer(pod, preferredContainer)
	if !ok {
		return nil, fmt.Errorf("pod %s has no containers", pod.Name)
	}
	targets := make([]ExecTarget, 0, len(ShellFallback))
	for _, shell := range ShellFallback {
		targets = append(targets, ExecTarget{
			Namespace: pod.Namespace,
			Pod:       pod.Name,
			Container: container,
			Command:   []string{shell},
		})
	}
	return targets, nil
}

func pickContainer(pod corev1.Pod, preferred string) (string, bool) {
	if preferred != "" {
		for _, container := range pod.Spec.Containers {
			if container.Name == preferred {
				return preferred, true
			}
		}
	}
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name, true
	}
	return "", false
}

func NeedsContainerPicker(pod corev1.Pod) bool {
	return len(pod.Spec.Containers) > 1
}

func RunExec(ctx context.Context, cfg *rest.Config, client kubernetes.Interface, target ExecTarget, stdin io.Reader, stdout, stderr io.Writer) error {
	if cfg == nil {
		return fmt.Errorf("rest config is required for exec")
	}
	if client == nil {
		return fmt.Errorf("kubernetes client is required for exec")
	}
	request := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(target.Pod).
		Namespace(target.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: target.Container,
			Command:   target.Command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       stdin != nil,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(cfg, http.MethodPost, request.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    stdin != nil,
	})
}
