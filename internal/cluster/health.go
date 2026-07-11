//revive:disable:exported package-comments
package cluster

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HealthProblem struct {
	Severity string
	Resource string
	Message  string
}

type HealthSnapshot struct {
	GeneratedAt       time.Time
	NodesTotal        int
	NodesReady        int
	PodsTotal         int
	PodsRunning       int
	PodsSucceeded     int
	PodsPending       int
	PodsFailed        int
	ContainerRestarts int32
	WorkloadsTotal    int
	WorkloadsReady    int
	WarningEvents     int
	Problems          []HealthProblem
	Errors            []string
}

func ClusterHealth(ctx context.Context, client *Client, namespace string) (HealthSnapshot, error) {
	snapshot := HealthSnapshot{GeneratedAt: time.Now()}
	if client == nil || client.Clientset == nil {
		return snapshot, fmt.Errorf("kubernetes clientset is required")
	}
	options := metav1.ListOptions{Limit: 5_000}
	nodes, err := client.Clientset.CoreV1().Nodes().List(ctx, options)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "nodes: "+err.Error())
	} else {
		snapshot.NodesTotal = len(nodes.Items)
		for _, node := range nodes.Items {
			if nodeReady(node) {
				snapshot.NodesReady++
			} else {
				snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "critical", Resource: "Node/" + node.Name, Message: "not Ready"})
			}
		}
	}
	pods, err := client.Clientset.CoreV1().Pods(namespace).List(ctx, options)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "pods: "+err.Error())
	} else {
		snapshot.PodsTotal = len(pods.Items)
		for _, pod := range pods.Items {
			switch pod.Status.Phase {
			case corev1.PodRunning:
				snapshot.PodsRunning++
			case corev1.PodSucceeded:
				snapshot.PodsSucceeded++
			case corev1.PodPending:
				snapshot.PodsPending++
				snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "warning", Resource: "Pod/" + pod.Namespace + "/" + pod.Name, Message: "Pending"})
			case corev1.PodFailed:
				snapshot.PodsFailed++
				snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "critical", Resource: "Pod/" + pod.Namespace + "/" + pod.Name, Message: "Failed"})
			}
			for _, status := range pod.Status.ContainerStatuses {
				snapshot.ContainerRestarts += status.RestartCount
				if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
					snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "warning", Resource: "Pod/" + pod.Namespace + "/" + pod.Name, Message: status.Name + ": " + status.State.Waiting.Reason})
				}
			}
		}
	}
	deployments, err := client.Clientset.AppsV1().Deployments(namespace).List(ctx, options)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "deployments: "+err.Error())
	} else {
		for _, workload := range deployments.Items {
			snapshot.addDeployment(workload)
		}
	}
	statefulSets, err := client.Clientset.AppsV1().StatefulSets(namespace).List(ctx, options)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "statefulsets: "+err.Error())
	} else {
		for _, workload := range statefulSets.Items {
			snapshot.WorkloadsTotal++
			desired := int32(1)
			if workload.Spec.Replicas != nil {
				desired = *workload.Spec.Replicas
			}
			if workload.Status.ReadyReplicas >= desired {
				snapshot.WorkloadsReady++
			} else {
				snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "warning", Resource: "StatefulSet/" + workload.Namespace + "/" + workload.Name, Message: fmt.Sprintf("ready %d/%d", workload.Status.ReadyReplicas, desired)})
			}
		}
	}
	daemonSets, err := client.Clientset.AppsV1().DaemonSets(namespace).List(ctx, options)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "daemonsets: "+err.Error())
	} else {
		for _, workload := range daemonSets.Items {
			snapshot.WorkloadsTotal++
			if workload.Status.NumberReady >= workload.Status.DesiredNumberScheduled {
				snapshot.WorkloadsReady++
			} else {
				snapshot.Problems = append(snapshot.Problems, HealthProblem{Severity: "warning", Resource: "DaemonSet/" + workload.Namespace + "/" + workload.Name, Message: fmt.Sprintf("ready %d/%d", workload.Status.NumberReady, workload.Status.DesiredNumberScheduled)})
			}
		}
	}
	events, err := client.Clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 250})
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "events: "+err.Error())
	} else {
		snapshot.WarningEvents = len(events.Items)
	}
	sort.SliceStable(snapshot.Problems, func(i, j int) bool {
		if snapshot.Problems[i].Severity != snapshot.Problems[j].Severity {
			return snapshot.Problems[i].Severity == "critical"
		}
		return snapshot.Problems[i].Resource < snapshot.Problems[j].Resource
	})
	if len(snapshot.Problems) > 100 {
		snapshot.Problems = snapshot.Problems[:100]
	}
	return snapshot, nil
}

func (s *HealthSnapshot) addDeployment(workload appsv1.Deployment) {
	s.WorkloadsTotal++
	desired := int32(1)
	if workload.Spec.Replicas != nil {
		desired = *workload.Spec.Replicas
	}
	if workload.Status.ReadyReplicas >= desired {
		s.WorkloadsReady++
		return
	}
	s.Problems = append(s.Problems, HealthProblem{Severity: "warning", Resource: "Deployment/" + workload.Namespace + "/" + workload.Name, Message: fmt.Sprintf("ready %d/%d", workload.Status.ReadyReplicas, desired)})
}

func nodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
