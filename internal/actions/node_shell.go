package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// PrepareNodeShell creates a single explicitly requested privileged helper and
// always attempts UID-preconditioned cleanup on failure or session completion.
func PrepareNodeShell(ctx context.Context, client kubernetes.Interface, nodeName string, nodeUID types.UID, namespace, image string) (ExecTarget, func() error, error) {
	if nodeUID == "" {
		return ExecTarget{}, nil, fmt.Errorf("node identity is required")
	}
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return ExecTarget{}, nil, err
	}
	if node.UID != nodeUID {
		return ExecTarget{}, nil, fmt.Errorf("node was replaced")
	}
	if node.Status.NodeInfo.OperatingSystem != "" && node.Status.NodeInfo.OperatingSystem != "linux" {
		return ExecTarget{}, nil, fmt.Errorf("node shell requires a Linux node")
	}
	if namespace == "" {
		namespace = "default"
	}
	if image == "" {
		image = "busybox:1.37.0"
	}
	privileged := true
	zero := int64(0)
	deadline := int64(3600)
	pod, err := client.CoreV1().Pods(namespace).Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "astrotui-node-shell-", Labels: map[string]string{"app.kubernetes.io/managed-by": "astrotui-node-shell"}}, Spec: corev1.PodSpec{NodeName: nodeName, HostPID: true, HostNetwork: true, RestartPolicy: corev1.RestartPolicyNever, ActiveDeadlineSeconds: &deadline, TerminationGracePeriodSeconds: &zero, Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}}, Containers: []corev1.Container{{Name: "shell", Image: image, Command: []string{"sh", "-c", "sleep 3600"}, SecurityContext: &corev1.SecurityContext{Privileged: &privileged, RunAsUser: &zero}}}}}, metav1.CreateOptions{})
	if err != nil {
		return ExecTarget{}, nil, err
	}
	cleanup := func() error {
		c, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		err := client.CoreV1().Pods(namespace).Delete(c, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero, Preconditions: &metav1.Preconditions{UID: &pod.UID}})
		if err != nil {
			return fmt.Errorf("cleanup %s/%s failed: %w", namespace, pod.Name, err)
		}
		return nil
	}
	err = wait.PollUntilContextTimeout(ctx, 250*time.Millisecond, 60*time.Second, true, func(c context.Context) (bool, error) {
		if err := c.Err(); err != nil {
			return false, err
		}
		current, e := client.CoreV1().Pods(namespace).Get(c, pod.Name, metav1.GetOptions{})
		if e != nil {
			return false, e
		}
		if current.UID != pod.UID {
			return false, fmt.Errorf("helper pod was replaced")
		}
		if current.Status.Phase == corev1.PodFailed || current.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("helper exited before shell: %s", current.Status.Phase)
		}
		for _, s := range current.Status.ContainerStatuses {
			if s.Name == "shell" && s.State.Running != nil {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return ExecTarget{}, nil, errors.Join(err, cleanup())
	}
	if err := ctx.Err(); err != nil {
		return ExecTarget{}, nil, errors.Join(err, cleanup())
	}
	current, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil || current.UID != nodeUID {
		if err == nil {
			err = fmt.Errorf("node was replaced during preparation")
		}
		return ExecTarget{}, nil, errors.Join(err, cleanup())
	}
	return ExecTarget{UID: pod.UID, Namespace: namespace, Pod: pod.Name, Container: "shell", Command: []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "sh"}}, cleanup, nil
}
