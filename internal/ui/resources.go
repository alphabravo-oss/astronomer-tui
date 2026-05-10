//revive:disable:exported package-comments
package ui

import (
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceAlias struct {
	Aliases []string
	GVR     schema.GroupVersionResource
}

func ResourceAliases() []ResourceAlias {
	return []ResourceAlias{
		{Aliases: []string{"po", "pod", "pods"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}},
		{Aliases: []string{"dp", "deploy", "deployment", "deployments"}, GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{Aliases: []string{"svc", "service", "services"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "services"}},
		{Aliases: []string{"ep", "endpoint", "endpoints"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}},
		{Aliases: []string{"cm", "configmap", "configmaps"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}},
		{Aliases: []string{"sec", "secret", "secrets"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}},
		{Aliases: []string{"ns", "namespace", "namespaces"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
		{Aliases: []string{"no", "node", "nodes"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}},
		{Aliases: []string{"ev", "event", "events"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "events"}},
		{Aliases: []string{"sts", "statefulset", "statefulsets"}, GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
		{Aliases: []string{"ds", "daemonset", "daemonsets"}, GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
		{Aliases: []string{"job", "jobs"}, GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
		{Aliases: []string{"cj", "cronjob", "cronjobs"}, GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
		{Aliases: []string{"rs", "replicaset", "replicasets"}, GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}},
		{Aliases: []string{"hpa", "horizontalpodautoscaler", "horizontalpodautoscalers"}, GVR: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}},
		{Aliases: []string{"pdb", "poddisruptionbudget", "poddisruptionbudgets"}, GVR: schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}},
		{Aliases: []string{"ing", "ingress", "ingresses"}, GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
		{Aliases: []string{"epslice", "endpointslice", "endpointslices"}, GVR: schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}},
		{Aliases: []string{"netpol", "networkpolicy", "networkpolicies"}, GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}},
		{Aliases: []string{"pv", "persistentvolume", "persistentvolumes"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}},
		{Aliases: []string{"pvc", "persistentvolumeclaim", "persistentvolumeclaims"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}},
		{Aliases: []string{"sc", "storageclass", "storageclasses"}, GVR: schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}},
		{Aliases: []string{"va", "volumeattachment", "volumeattachments"}, GVR: schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "volumeattachments"}},
		{Aliases: []string{"role", "roles"}, GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}},
		{Aliases: []string{"rb", "rolebinding", "rolebindings"}, GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}},
		{Aliases: []string{"cr", "clusterrole", "clusterroles"}, GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}},
		{Aliases: []string{"crb", "clusterrolebinding", "clusterrolebindings"}, GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}},
		{Aliases: []string{"sa", "serviceaccount", "serviceaccounts"}, GVR: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}},
		{Aliases: []string{"apiservice", "apiservices"}, GVR: schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}},
		{Aliases: []string{"lease", "leases"}, GVR: schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}},
	}
}

func ResourceAliasEntries() []string {
	var entries []string
	for _, item := range ResourceAliases() {
		entries = append(entries, ":"+item.Aliases[len(item.Aliases)-1])
	}
	sort.Strings(entries)
	return entries
}

func resourceCommand(command string) (schema.GroupVersionResource, bool) {
	normalized := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(command)), ":")
	for _, item := range ResourceAliases() {
		for _, alias := range item.Aliases {
			if normalized == alias {
				return item.GVR, true
			}
		}
	}
	if gvr, ok := explicitGVRCommand(normalized); ok {
		return gvr, true
	}
	return schema.GroupVersionResource{}, false
}

func explicitGVRCommand(command string) (schema.GroupVersionResource, bool) {
	parts := strings.Split(command, "/")
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return schema.GroupVersionResource{}, false
		}
		return schema.GroupVersionResource{Version: parts[0], Resource: parts[1]}, true
	case 3:
		if parts[1] == "" || parts[2] == "" {
			return schema.GroupVersionResource{}, false
		}
		return schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}, true
	default:
		return schema.GroupVersionResource{}, false
	}
}
