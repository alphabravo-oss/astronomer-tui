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
	"k8s.io/client-go/kubernetes"
)

type PodLogTarget struct {
	Namespace string
	Pod       string
	Container string
}

type RequestOptions struct {
	TailLines    *int64
	SinceSeconds *int64
	Follow       bool
	Previous     bool
	Timestamps   bool
}

func ContainerNames(pod corev1.Pod) []string {
	names := make([]string, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	for _, container := range pod.Spec.Containers {
		names = append(names, container.Name)
	}
	return names
}

func DefaultContainer(pod corev1.Pod) (string, bool) {
	names := ContainerNames(pod)
	if len(names) == 0 {
		return "", false
	}
	return names[0], true
}

func TargetsForPods(pods []corev1.Pod) []PodLogTarget {
	targets := make([]PodLogTarget, 0, len(pods))
	for _, pod := range pods {
		container, _ := DefaultContainer(pod)
		targets = append(targets, PodLogTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container})
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
	request := &corev1.PodLogOptions{
		Container:    target.Container,
		Follow:       opts.Follow,
		Previous:     opts.Previous,
		TailLines:    opts.TailLines,
		SinceSeconds: opts.SinceSeconds,
		Timestamps:   opts.Timestamps,
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
