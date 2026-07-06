//revive:disable:exported package-comments
package views

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// StateColor returns the semantic row color for an object based on its runtime
// state, or "" for the default foreground. Mirrors k9s row coloring: problems
// are red, transitional states yellow, terminating/completed muted.
func StateColor(gvr schema.GroupVersionResource, u *unstructured.Unstructured) string {
	obj := u.Object
	if deletion := nestedString(obj, "metadata", "deletionTimestamp"); deletion != "" {
		return "muted" // Terminating
	}
	switch gvr.Resource {
	case "pods":
		return podRowColor(obj)
	case "nodes":
		return nodeRowColor(obj)
	case "events":
		if strings.EqualFold(nestedString(obj, "type"), "Warning") {
			return "warn"
		}
		return ""
	case "deployments", "statefulsets", "daemonsets", "replicasets":
		return workloadRowColor(obj)
	case "jobs":
		return jobRowColor(obj)
	case "persistentvolumeclaims", "persistentvolumes":
		return boundRowColor(obj)
	default:
		return ""
	}
}

func podRowColor(obj map[string]any) string {
	status := podStatus(obj)
	switch status {
	case "Running", "Completed", "Succeeded":
		if status == "Running" && !podFullyReady(obj) {
			return "warn"
		}
		return ""
	case "Pending", "ContainerCreating", "PodInitializing", "Terminating":
		return "warn"
	case "":
		return ""
	default:
		// CrashLoopBackOff, ImagePullBackOff, Error, Init:*, Evicted, ...
		return "error"
	}
}

func podFullyReady(obj map[string]any) bool {
	statuses, ok, _ := unstructured.NestedSlice(obj, "status", "containerStatuses")
	if !ok || len(statuses) == 0 {
		return false
	}
	for _, item := range statuses {
		s, ok := item.(map[string]any)
		if !ok {
			return false
		}
		if ready, _, _ := unstructured.NestedBool(s, "ready"); !ready {
			return false
		}
	}
	return true
}

func nodeRowColor(obj map[string]any) string {
	if unschedulable, ok, _ := unstructured.NestedBool(obj, "spec", "unschedulable"); ok && unschedulable {
		return "warn"
	}
	if nodeReadyStatus(obj) == "NotReady" {
		return "error"
	}
	return ""
}

func workloadRowColor(obj map[string]any) string {
	ready, hasReady, _ := unstructured.NestedInt64(obj, "status", "readyReplicas")
	desired, hasDesired, _ := unstructured.NestedInt64(obj, "spec", "replicas")
	if !hasDesired {
		// DaemonSets use desiredNumberScheduled/numberReady.
		desired, hasDesired, _ = unstructured.NestedInt64(obj, "status", "desiredNumberScheduled")
		ready, hasReady, _ = unstructured.NestedInt64(obj, "status", "numberReady")
	}
	if !hasDesired {
		return ""
	}
	if !hasReady {
		ready = 0
	}
	if desired == 0 {
		return "muted"
	}
	if ready < desired {
		return "warn"
	}
	return ""
}

func jobRowColor(obj map[string]any) string {
	if failed, ok, _ := unstructured.NestedInt64(obj, "status", "failed"); ok && failed > 0 {
		return "error"
	}
	if succeeded, ok, _ := unstructured.NestedInt64(obj, "status", "succeeded"); ok && succeeded > 0 {
		return "muted"
	}
	return ""
}

func boundRowColor(obj map[string]any) string {
	phase := nestedString(obj, "status", "phase")
	switch phase {
	case "Bound", "Available":
		return ""
	case "Pending":
		return "warn"
	case "Failed", "Lost":
		return "error"
	default:
		return ""
	}
}
