//revive:disable:exported package-comments
package logs

import (
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type PodLogTarget struct {
	PodUID    types.UID
	Namespace string
	Pod       string
	Container string
}

type RequestOptions struct {
	TailLines  *int64
	Follow     bool
	Previous   bool
	Timestamps bool
	SinceTime  *metav1.Time
}

func ContainerNames(pod corev1.Pod) []string {
	names := make([]string, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	for _, container := range pod.Spec.Containers {
		names = append(names, container.Name)
	}
	for _, container := range pod.Spec.EphemeralContainers {
		names = append(names, container.Name)
	}
	return names
}

func DefaultContainer(pod corev1.Pod) (string, bool) {
	if chosen := pod.Annotations["kubectl.kubernetes.io/default-container"]; chosen != "" {
		for _, c := range pod.Spec.Containers {
			if c.Name == chosen {
				return chosen, true
			}
		}
	}
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name, true
	}
	return "", false
}

func TargetsForPods(pods []corev1.Pod) []PodLogTarget {
	targets := make([]PodLogTarget, 0, len(pods))
	for _, pod := range pods {
		container, _ := DefaultContainer(pod)
		targets = append(targets, PodLogTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container, PodUID: pod.UID})
	}
	return targets
}

func PodsForSelector(ctx context.Context, client kubernetes.Interface, namespace string, selector labels.Selector) ([]corev1.Pod, error) {
	list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func StreamPodLogs(ctx context.Context, client kubernetes.Interface, target PodLogTarget, opts RequestOptions, writer io.Writer) error {
	if target.PodUID != "" {
		pod, err := client.CoreV1().Pods(target.Namespace).Get(ctx, target.Pod, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if pod.UID != target.PodUID {
			return fmt.Errorf("pod was replaced; refresh before opening logs")
		}
	}
	request := &corev1.PodLogOptions{
		Container:  target.Container,
		Follow:     opts.Follow,
		Previous:   opts.Previous,
		TailLines:  opts.TailLines,
		Timestamps: opts.Timestamps, SinceTime: opts.SinceTime,
	}
	stream, err := client.CoreV1().Pods(target.Namespace).GetLogs(target.Pod, request).Stream(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	_, err = io.Copy(writer, stream)
	return err
}

func ContainerPickerHint(pod corev1.Pod) string {
	names := ContainerNames(pod)
	if len(names) <= 1 {
		return ""
	}
	return fmt.Sprintf("containers: %s", strings.Join(names, ", "))
}

func AllTargetsForPods(pods []corev1.Pod) []PodLogTarget {
	var out []PodLogTarget
	for _, p := range pods {
		for _, c := range ContainerNames(p) {
			out = append(out, PodLogTarget{Namespace: p.Namespace, Pod: p.Name, Container: c, PodUID: p.UID})
		}
	}
	return out
}
