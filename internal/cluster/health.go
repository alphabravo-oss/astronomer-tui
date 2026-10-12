package cluster

import (
	"context"
	"fmt"
	"sort"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type HealthGroup struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

var HealthGroups = []HealthGroup{
	{schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, false},
	{schema.GroupVersionResource{Version: "v1", Resource: "pods"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, true},
	{schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, true},
	{schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, true},
}

type HealthCount struct {
	HealthGroup
	Total, Healthy, Warning, Unknown int
	Err                              error
}

func ReadHealth(ctx context.Context, client dynamic.Interface, ns string) []HealthCount {
	out := make([]HealthCount, len(HealthGroups))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for i, g := range HealthGroups {
		wg.Add(1)
		go func(i int, g HealthGroup) {
			defer wg.Done()
			out[i].HealthGroup = g
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				out[i].Err = ctx.Err()
				return
			}
			var list *unstructured.UnstructuredList
			var err error
			if g.Namespaced {
				list, err = client.Resource(g.GVR).Namespace(ns).List(ctx, metav1.ListOptions{})
			} else {
				list, err = client.Resource(g.GVR).List(ctx, metav1.ListOptions{})
			}
			out[i].Err = err
			if err != nil {
				return
			}
			out[i].Total = len(list.Items)
			for j := range list.Items {
				switch ResourceHealth(&list.Items[j]) {
				case "healthy":
					out[i].Healthy++
				case "warning":
					out[i].Warning++
				default:
					out[i].Unknown++
				}
			}
		}(i, g)
	}
	wg.Wait()
	return out
}
func ResourceHealth(u *unstructured.Unstructured) string {
	if u.GetDeletionTimestamp() != nil {
		return "warning"
	}
	str := func(path ...string) string { v, _, _ := unstructured.NestedString(u.Object, path...); return v }
	num := func(path ...string) int64 { v, _, _ := unstructured.NestedInt64(u.Object, path...); return v }
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	condition := func(kind string) string {
		for _, raw := range conditions {
			c, ok := raw.(map[string]any)
			if ok && c["type"] == kind {
				v, _ := c["status"].(string)
				return v
			}
		}
		return ""
	}
	switch u.GetKind() {
	case "Node":
		if condition("Ready") == "True" {
			return "healthy"
		}
		return "warning"
	case "Pod":
		if str("status", "phase") == "Succeeded" {
			return "healthy"
		}
		if str("status", "phase") == "Running" && condition("Ready") == "True" {
			return "healthy"
		}
		return "warning"
	case "PersistentVolumeClaim":
		if str("status", "phase") == "Bound" {
			return "healthy"
		}
		return "warning"
	case "Job":
		if condition("Complete") == "True" {
			return "healthy"
		}
		if condition("Failed") == "True" {
			return "warning"
		}
		return "unknown"
	case "Deployment", "StatefulSet", "ReplicaSet":
		desired, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		if !found {
			desired = 1
		}
		if num("status", "observedGeneration") < u.GetGeneration() {
			return "unknown"
		}
		if num("status", "readyReplicas") >= desired {
			return "healthy"
		}
		return "warning"
	case "DaemonSet":
		if num("status", "observedGeneration") < u.GetGeneration() {
			return "unknown"
		}
		if num("status", "numberReady") >= num("status", "desiredNumberScheduled") {
			return "healthy"
		}
		return "warning"
	}
	return "unknown"
}

type GraphObject struct {
	GVR    schema.GroupVersionResource
	Object *unstructured.Unstructured
}
type GraphSnapshot struct {
	Objects  []GraphObject
	Warnings []string
}

// Workload trees resolve ownership by UID, never by a reused name.
func ReadGraph(ctx context.Context, client dynamic.Interface, roots schema.GroupVersionResource, ns string) GraphSnapshot {
	groups := []schema.GroupVersionResource{roots, {Version: "v1", Resource: "pods"}, {Group: "apps", Version: "v1", Resource: "replicasets"}, {Group: "batch", Version: "v1", Resource: "jobs"}, {Version: "v1", Resource: "persistentvolumeclaims"}, {Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}}
	seen := map[schema.GroupVersionResource]bool{}
	var out GraphSnapshot
	for _, g := range groups {
		if seen[g] {
			continue
		}
		seen[g] = true
		var list *unstructured.UnstructuredList
		var err error
		if g.Resource == "nodes" {
			list, err = client.Resource(g).List(ctx, metav1.ListOptions{})
		} else {
			list, err = client.Resource(g).Namespace(ns).List(ctx, metav1.ListOptions{})
		}
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", g.Resource, err))
			continue
		}
		for i := range list.Items {
			out.Objects = append(out.Objects, GraphObject{g, list.Items[i].DeepCopy()})
		}
	}
	sort.SliceStable(out.Objects, func(i, j int) bool {
		a, b := out.Objects[i].Object, out.Objects[j].Object
		return a.GetNamespace()+"/"+a.GetKind()+"/"+a.GetName() < b.GetNamespace()+"/"+b.GetKind()+"/"+b.GetName()
	})
	return out
}
