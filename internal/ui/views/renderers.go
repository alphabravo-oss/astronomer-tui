//revive:disable:exported package-comments
package views

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

type Column struct {
	Name string
}

type Cell struct {
	Text string
}

type DetailContent struct {
	Title string
	Body  string
}

type Renderer interface {
	GVR() schema.GroupVersionResource
	Header() []Column
	Row(*unstructured.Unstructured) []Cell
	Detail(*unstructured.Unstructured) DetailContent
}

type GenericRenderer struct {
	Resource schema.GroupVersionResource
	Columns  []string
	Paths    map[string]string
}

func (g GenericRenderer) GVR() schema.GroupVersionResource { return g.Resource }

func (g GenericRenderer) Header() []Column {
	cols := g.Columns
	if len(cols) == 0 {
		cols = []string{"NAME", "NAMESPACE", "AGE"}
	}
	out := make([]Column, len(cols))
	for i, col := range cols {
		out[i] = Column{Name: col}
	}
	return out
}

func (g GenericRenderer) Row(u *unstructured.Unstructured) []Cell {
	cols := g.Columns
	if len(cols) == 0 {
		cols = []string{"NAME", "NAMESPACE", "AGE"}
	}
	out := make([]Cell, 0, len(cols))
	for _, col := range cols {
		switch strings.ToUpper(col) {
		case "NAME":
			out = append(out, Cell{Text: u.GetName()})
		case "NAMESPACE", "NS":
			out = append(out, Cell{Text: u.GetNamespace()})
		case "AGE":
			out = append(out, Cell{Text: age(u.GetCreationTimestamp().Time)})
		default:
			out = append(out, Cell{Text: g.valueFor(u, col)})
		}
	}
	return out
}

func (g GenericRenderer) Detail(u *unstructured.Unstructured) DetailContent {
	body, err := YAMLDetail(u)
	if err != nil {
		body = fmt.Sprintf("%#v", u.Object)
	}
	title := u.GetKind() + "/" + u.GetName()
	if u.GetKind() == "" {
		title = g.Resource.Resource + "/" + u.GetName()
	}
	return DetailContent{Title: title, Body: body}
}

func YAMLDetail(u *unstructured.Unstructured) (string, error) {
	data, err := json.Marshal(u.Object)
	if err != nil {
		return "", err
	}
	out, err := yaml.JSONToYAML(data)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func RendererForCRD(crd *apiextensionsv1.CustomResourceDefinition, version string) Renderer {
	if version == "" {
		for _, item := range crd.Spec.Versions {
			if item.Served {
				version = item.Name
				break
			}
		}
	}
	columns := []string{"NAME"}
	paths := map[string]string{}
	for _, item := range crd.Spec.Versions {
		if item.Name != version {
			continue
		}
		for _, column := range item.AdditionalPrinterColumns {
			columns = append(columns, strings.ToUpper(column.Name))
			paths[strings.ToUpper(column.Name)] = column.JSONPath
		}
		break
	}
	if len(columns) == 1 {
		columns = append(columns, "AGE")
	}
	return GenericRenderer{
		Resource: schema.GroupVersionResource{Group: crd.Spec.Group, Version: version, Resource: crd.Spec.Names.Plural},
		Columns:  columns,
		Paths:    paths,
	}
}

func Builtins() map[schema.GroupVersionResource]Renderer {
	renderers := []Renderer{
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Columns: []string{"NAMESPACE", "NAME", "VS", "PF", "READY", "STATUS", "RESTARTS", "LAST RESTART", "CPU", "CPU/RL", "%CPU/R", "%CPU/L", "MEM", "MEM/RL", "%MEM/R", "%MEM/L", "GPU/RL", "IP", "NODE", "SERVICE-ACCOUNT", "NOMINATED NODE", "READINESS GATES", "QOS", "LABELS", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Columns: []string{"NAMESPACE", "NAME", "VS", "READY", "UP-TO-DATE", "AVAILABLE", "LABELS", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "services"}, Columns: []string{"NAMESPACE", "NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "SELECTOR", "PORTS", "LABELS", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}, Columns: []string{"NAMESPACE", "NAME", "ENDPOINTS", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Columns: []string{"NAMESPACE", "NAME", "DATA", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, Columns: []string{"NAMESPACE", "NAME", "TYPE", "DATA", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, Columns: []string{"NAME", "STATUS", "LABELS", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, Columns: []string{"NAME", "STATUS", "ROLE", "ARCH", "TAINTS", "VERSION", "OS-IMAGE", "KERNEL", "INTERNAL-IP", "EXTERNAL-IP", "PODS", "CPU", "CPU/A", "%CPU", "MEM", "MEM/A", "%MEM", "GPU/A", "GPU/C", "SH-GPU/A", "SH-GPU/C", "LABELS", "VALID", "AGE"}},
		GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "events"}, Columns: []string{"LAST SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"}},
	}
	for name, columns := range builtinColumns() {
		renderers = append(renderers, GenericRenderer{Resource: inferGVR(name), Columns: columns})
	}
	out := map[schema.GroupVersionResource]Renderer{}
	for _, renderer := range renderers {
		out[renderer.GVR()] = renderer
	}
	return out
}

func builtinColumns() map[string][]string {
	return map[string][]string{
		"statefulsets":             {"NAMESPACE", "NAME", "VS", "READY", "SELECTOR", "SERVICE", "CONTAINERS", "IMAGES", "LABELS", "VALID", "AGE"},
		"daemonsets":               {"NAMESPACE", "NAME", "VS", "DESIRED", "CURRENT", "READY", "UP-TO-DATE", "AVAILABLE", "LABELS", "VALID", "AGE"},
		"jobs":                     {"NAMESPACE", "NAME", "VS", "COMPLETIONS", "DURATION", "SELECTOR", "CONTAINERS", "IMAGES", "VALID", "AGE"},
		"cronjobs":                 {"NAMESPACE", "NAME", "VS", "SCHEDULE", "SUSPEND", "ACTIVE", "LAST_SCHEDULE", "SELECTOR", "CONTAINERS", "IMAGES", "LABELS", "VALID", "AGE"},
		"replicasets":              {"NAMESPACE", "NAME", "VS", "DESIRED", "CURRENT", "READY", "CONTAINERS", "IMAGES", "SELECTOR", "VALID", "AGE"},
		"horizontalpodautoscalers": {"NAME", "NAMESPACE", "REFERENCE", "TARGETS", "MINPODS", "MAXPODS", "REPLICAS", "AGE"},
		"poddisruptionbudgets":     {"NAMESPACE", "NAME", "MIN-AVAILABLE", "MAX-UNAVAILABLE", "ALLOWED-DISRUPTIONS", "CURRENT", "DESIRED", "EXPECTED", "LABELS", "VALID", "AGE"},
		"ingresses":                {"NAME", "NAMESPACE", "CLASS", "HOSTS", "ADDRESS", "PORTS", "AGE"},
		"endpointslices":           {"NAMESPACE", "NAME", "ADDRESSTYPE", "PORTS", "ENDPOINTS", "AGE"},
		"networkpolicies":          {"NAMESPACE", "NAME", "POD-SELECTOR", "ING-SELECTOR", "ING-PORTS", "ING-BLOCK", "EGR-SELECTOR", "EGR-PORTS", "EGR-BLOCK", "LABELS", "VALID", "AGE"},
		"persistentvolumes":        {"NAME", "CAPACITY", "ACCESS MODES", "RECLAIM POLICY", "STATUS", "CLAIM", "STORAGECLASS", "REASON", "VOLUMEMODE", "LABELS", "VALID", "AGE"},
		"persistentvolumeclaims":   {"NAMESPACE", "NAME", "STATUS", "VOLUME", "CAPACITY", "ACCESS MODES", "STORAGECLASS", "LABELS", "VALID", "AGE"},
		"storageclasses":           {"NAME", "PROVISIONER", "RECLAIMPOLICY", "VOLUMEBINDINGMODE", "ALLOWVOLUMEEXPANSION", "LABELS", "VALID", "AGE"},
		"volumeattachments":        {"NAME", "ATTACHER", "PV", "NODE", "ATTACHED", "AGE"},
		"roles":                    {"NAMESPACE", "NAME", "LABELS", "VALID", "AGE"},
		"rolebindings":             {"NAMESPACE", "NAME", "ROLE", "KIND", "SUBJECTS", "LABELS", "VALID", "AGE"},
		"clusterroles":             {"NAME", "LABELS", "AGE"},
		"clusterrolebindings":      {"NAME", "CLUSTERROLE", "SUBJECT-KIND", "SUBJECTS", "LABELS", "AGE"},
		"serviceaccounts":          {"NAMESPACE", "NAME", "SECRET", "LABELS", "VALID", "AGE"},
		"apiservices":              {"NAME", "SERVICE", "AVAILABLE", "AGE"},
		"leases":                   {"NAME", "NAMESPACE", "HOLDER", "AGE"},
	}
}

func SortRows(rows [][]Cell, col int, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if col >= len(rows[i]) || col >= len(rows[j]) {
			return false
		}
		if desc {
			return rows[i][col].Text > rows[j][col].Text
		}
		return rows[i][col].Text < rows[j][col].Text
	})
}

func FilterRows(rows [][]Cell, query string) [][]Cell {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return rows
	}
	out := make([][]Cell, 0, len(rows))
	for _, row := range rows {
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell.Text), query) {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

func inferGVR(resource string) schema.GroupVersionResource {
	switch resource {
	case "statefulsets", "daemonsets", "deployments", "replicasets":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource}
	case "jobs", "cronjobs":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: resource}
	case "horizontalpodautoscalers":
		return schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: resource}
	case "poddisruptionbudgets":
		return schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: resource}
	case "ingresses", "networkpolicies":
		return schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: resource}
	case "endpointslices":
		return schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: resource}
	case "storageclasses", "volumeattachments":
		return schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: resource}
	case "roles", "rolebindings", "clusterroles", "clusterrolebindings":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: resource}
	case "apiservices":
		return schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: resource}
	case "leases":
		return schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: resource}
	default:
		return schema.GroupVersionResource{Version: "v1", Resource: resource}
	}
}

func field(obj map[string]any, name string) string {
	switch strings.ToUpper(name) {
	case "STATUS":
		if ready := nodeReadyStatus(obj); ready != "" {
			return ready
		}
		if v, ok, _ := unstructured.NestedString(obj, "status", "phase"); ok {
			return v
		}
	case "READY":
		if ready := podReady(obj); ready != "" {
			return ready
		}
		if ready, ok, _ := unstructured.NestedInt64(obj, "status", "readyReplicas"); ok {
			if replicas, ok, _ := unstructured.NestedInt64(obj, "status", "replicas"); ok {
				return fmt.Sprintf("%d/%d", ready, replicas)
			}
			return fmt.Sprintf("%d", ready)
		}
		if ready, ok, _ := unstructured.NestedInt64(obj, "status", "numberReady"); ok {
			if desired, ok, _ := unstructured.NestedInt64(obj, "status", "desiredNumberScheduled"); ok {
				return fmt.Sprintf("%d/%d", ready, desired)
			}
			return fmt.Sprintf("%d", ready)
		}
	case "RESTARTS":
		statuses, ok, _ := unstructured.NestedSlice(obj, "status", "containerStatuses")
		if ok {
			var total int64
			for _, item := range statuses {
				if m, ok := item.(map[string]any); ok {
					if count, ok, _ := unstructured.NestedInt64(m, "restartCount"); ok {
						total += count
					}
				}
			}
			return fmt.Sprintf("%d", total)
		}
	case "DESIRED", "REPLICAS":
		return firstInt(obj, []string{"status", "desiredNumberScheduled"}, []string{"spec", "replicas"}, []string{"status", "replicas"})
	case "CURRENT":
		return firstInt(obj, []string{"status", "currentNumberScheduled"}, []string{"status", "replicas"})
	case "AVAILABLE":
		if replicas := firstInt(obj, []string{"status", "availableReplicas"}); replicas != "" {
			return replicas
		}
		if number := firstInt(obj, []string{"status", "numberAvailable"}); number != "" {
			return number
		}
		return conditionStatus(obj, "Available")
	case "UP-TO-DATE":
		return firstInt(obj, []string{"status", "updatedReplicas"}, []string{"status", "updatedNumberScheduled"})
	case "DATA":
		if data, ok, _ := unstructured.NestedMap(obj, "data"); ok {
			return fmt.Sprintf("%d", len(data))
		}
	case "COMPLETIONS":
		succeeded := firstInt(obj, []string{"status", "succeeded"})
		completions := firstInt(obj, []string{"spec", "completions"})
		if completions != "" {
			return succeeded + "/" + completions
		}
		return succeeded
	case "DURATION":
		return durationBetween(obj, []string{"status", "startTime"}, []string{"status", "completionTime"})
	case "SCHEDULE":
		return nestedString(obj, "spec", "schedule")
	case "SUSPEND":
		if v, ok, _ := unstructured.NestedBool(obj, "spec", "suspend"); ok {
			return fmt.Sprintf("%t", v)
		}
	case "ACTIVE":
		active, ok, _ := unstructured.NestedSlice(obj, "status", "active")
		if ok {
			return fmt.Sprintf("%d", len(active))
		}
	case "LAST SCHEDULE", "LAST_SCHEDULE":
		return timestampString(obj, "status", "lastScheduleTime")
	case "REFERENCE":
		if kind := nestedString(obj, "spec", "scaleTargetRef", "kind"); kind != "" {
			return kind + "/" + nestedString(obj, "spec", "scaleTargetRef", "name")
		}
	case "TARGETS":
		return hpaTargets(obj)
	case "MINPODS":
		return firstInt(obj, []string{"spec", "minReplicas"})
	case "MAXPODS":
		return firstInt(obj, []string{"spec", "maxReplicas"})
	case "MIN AVAILABLE", "MIN-AVAILABLE":
		return firstScalar(obj, []string{"spec", "minAvailable"})
	case "MAX UNAVAILABLE", "MAX-UNAVAILABLE":
		return firstScalar(obj, []string{"spec", "maxUnavailable"})
	case "ALLOWED DISRUPTIONS", "ALLOWED-DISRUPTIONS":
		return firstInt(obj, []string{"status", "disruptionsAllowed"})
	case "EXPECTED":
		return firstInt(obj, []string{"status", "expectedPods"})
	case "TYPE":
		return firstString(obj, []string{"spec", "type"}, []string{"type"})
	case "CLUSTER-IP":
		return nestedString(obj, "spec", "clusterIP")
	case "PORTS":
		return ports(obj)
	case "ENDPOINTS":
		return endpoints(obj)
	case "CLASS":
		return nestedString(obj, "spec", "ingressClassName")
	case "HOSTS":
		return ingressHosts(obj)
	case "ADDRESS":
		return ingressAddress(obj)
	case "ADDRESSTYPE":
		return nestedString(obj, "addressType")
	case "POD-SELECTOR":
		if selector, ok, _ := unstructured.NestedStringMap(obj, "spec", "podSelector", "matchLabels"); ok {
			return labelMap(selector)
		}
	case "CAPACITY":
		return firstString(obj, []string{"spec", "capacity", "storage"}, []string{"status", "capacity", "storage"})
	case "VOLUME":
		return nestedString(obj, "spec", "volumeName")
	case "ACCESS MODES":
		return stringSlice(obj, "spec", "accessModes")
	case "RECLAIM POLICY", "RECLAIMPOLICY":
		return firstString(obj, []string{"spec", "persistentVolumeReclaimPolicy"}, []string{"reclaimPolicy"})
	case "CLAIM":
		if name := nestedString(obj, "spec", "claimRef", "name"); name != "" {
			namespace := nestedString(obj, "spec", "claimRef", "namespace")
			if namespace != "" {
				return namespace + "/" + name
			}
			return name
		}
	case "PROVISIONER":
		return nestedString(obj, "provisioner")
	case "VOLUMEBINDINGMODE":
		return nestedString(obj, "volumeBindingMode")
	case "ALLOWVOLUMEEXPANSION":
		if v, ok, _ := unstructured.NestedBool(obj, "allowVolumeExpansion"); ok {
			return fmt.Sprintf("%t", v)
		}
	case "ATTACHER":
		return nestedString(obj, "spec", "attacher")
	case "PV":
		return nestedString(obj, "spec", "source", "persistentVolumeName")
	case "NODE":
		return firstString(obj, []string{"spec", "nodeName"}, []string{"spec", "nodeName"})
	case "ATTACHED":
		if v, ok, _ := unstructured.NestedBool(obj, "status", "attached"); ok {
			return fmt.Sprintf("%t", v)
		}
	case "ROLE":
		if kind := nestedString(obj, "roleRef", "kind"); kind != "" {
			return kind + "/" + nestedString(obj, "roleRef", "name")
		}
		return nodeRoles(obj)
	case "CLUSTERROLE":
		return nestedString(obj, "roleRef", "name")
	case "SUBJECT-KIND":
		if subjects, ok, _ := unstructured.NestedSlice(obj, "subjects"); ok && len(subjects) > 0 {
			if subject, ok := subjects[0].(map[string]any); ok {
				return nestedString(subject, "kind")
			}
		}
	case "SUBJECTS":
		if subjects, ok, _ := unstructured.NestedSlice(obj, "subjects"); ok {
			names := make([]string, 0, len(subjects))
			for _, item := range subjects {
				if subject, ok := item.(map[string]any); ok {
					if name := nestedString(subject, "name"); name != "" {
						names = append(names, name)
					}
				}
			}
			return strings.Join(names, ",")
		}
	case "KIND":
		if kind := nestedString(obj, "roleRef", "kind"); kind != "" {
			return kind
		}
	case "SECRET", "SECRETS":
		secrets, ok, _ := unstructured.NestedSlice(obj, "secrets")
		if ok {
			return fmt.Sprintf("%d", len(secrets))
		}
	case "SERVICE":
		if name := nestedString(obj, "spec", "serviceName"); name != "" {
			return name
		}
		if name := nestedString(obj, "spec", "service", "name"); name != "" {
			return nestedString(obj, "spec", "service", "namespace") + "/" + name
		}
	case "HOLDER":
		return nestedString(obj, "spec", "holderIdentity")
	case "ROLES":
		return nodeRoles(obj)
	case "VERSION":
		return nestedString(obj, "status", "nodeInfo", "kubeletVersion")
	case "LAST SEEN":
		return firstString(obj, []string{"lastTimestamp"}, []string{"eventTime"})
	case "REASON":
		return nestedString(obj, "reason")
	case "OBJECT":
		if kind := nestedString(obj, "involvedObject", "kind"); kind != "" {
			return kind + "/" + nestedString(obj, "involvedObject", "name")
		}
	case "MESSAGE":
		return nestedString(obj, "message")
	case "LABELS":
		if labels, ok, _ := unstructured.NestedStringMap(obj, "metadata", "labels"); ok {
			return labelMap(labels)
		}
	case "STORAGECLASS":
		return firstString(obj, []string{"spec", "storageClassName"}, []string{"storageClassName"})
	case "VOLUMEMODE":
		return nestedString(obj, "spec", "volumeMode")
	case "EXTERNAL-IP":
		return firstString(obj, []string{"spec", "externalIP"}, []string{"status", "loadBalancer", "ingress", "0", "ip"})
	case "SELECTOR":
		if selector, ok, _ := unstructured.NestedStringMap(obj, "spec", "selector", "matchLabels"); ok {
			return labelMap(selector)
		}
	}
	if v, ok, _ := unstructured.NestedString(obj, strings.ToLower(name)); ok {
		return v
	}
	return ""
}

func firstInt(obj map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if v, ok, _ := unstructured.NestedInt64(obj, path...); ok {
			return fmt.Sprintf("%d", v)
		}
	}
	return ""
}

func firstScalar(obj map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if v, ok, _ := unstructured.NestedFieldNoCopy(obj, path...); ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func nestedString(obj map[string]any, path ...string) string {
	if v, ok, _ := unstructured.NestedString(obj, path...); ok {
		return v
	}
	return ""
}

func firstString(obj map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if value := nestedString(obj, path...); value != "" {
			return value
		}
	}
	return ""
}

func podReady(obj map[string]any) string {
	statuses, ok, _ := unstructured.NestedSlice(obj, "status", "containerStatuses")
	if !ok || len(statuses) == 0 {
		return ""
	}
	var ready int
	for _, item := range statuses {
		status, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if value, ok, _ := unstructured.NestedBool(status, "ready"); ok && value {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(statuses))
}

func nodeReadyStatus(obj map[string]any) string {
	conditions, ok, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !ok {
		return ""
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || nestedString(condition, "type") != "Ready" {
			continue
		}
		if nestedString(condition, "status") == "True" {
			return "Ready"
		}
		return "NotReady"
	}
	return ""
}

func nodeRoles(obj map[string]any) string {
	labels, ok, _ := unstructured.NestedStringMap(obj, "metadata", "labels")
	if !ok {
		return ""
	}
	var roles []string
	for key := range labels {
		if strings.HasPrefix(key, "node-role.kubernetes.io/") {
			role := strings.TrimPrefix(key, "node-role.kubernetes.io/")
			if role == "" {
				role = "control-plane"
			}
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

func ports(obj map[string]any) string {
	items, ok, _ := unstructured.NestedSlice(obj, "spec", "ports")
	if !ok {
		items, ok, _ = unstructured.NestedSlice(obj, "ports")
	}
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		port, ok := item.(map[string]any)
		if !ok {
			continue
		}
		value := firstInt(port, []string{"port"})
		if value == "" {
			value = firstInt(port, []string{"containerPort"})
		}
		protocol := nestedString(port, "protocol")
		if protocol != "" {
			value += "/" + protocol
		}
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, ",")
}

func endpoints(obj map[string]any) string {
	if subsets, ok, _ := unstructured.NestedSlice(obj, "subsets"); ok {
		var count int
		for _, item := range subsets {
			subset, ok := item.(map[string]any)
			if !ok {
				continue
			}
			addresses, _, _ := unstructured.NestedSlice(subset, "addresses")
			count += len(addresses)
		}
		return fmt.Sprintf("%d", count)
	}
	if items, ok, _ := unstructured.NestedSlice(obj, "endpoints"); ok {
		return fmt.Sprintf("%d", len(items))
	}
	return ""
}

func ingressHosts(obj map[string]any) string {
	rules, ok, _ := unstructured.NestedSlice(obj, "spec", "rules")
	if !ok {
		return ""
	}
	var hosts []string
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if host := nestedString(rule, "host"); host != "" {
			hosts = append(hosts, host)
		}
	}
	return strings.Join(hosts, ",")
}

func ingressAddress(obj map[string]any) string {
	items, ok, _ := unstructured.NestedSlice(obj, "status", "loadBalancer", "ingress")
	if !ok {
		return ""
	}
	var addresses []string
	for _, item := range items {
		ingress, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if value := firstString(ingress, []string{"ip"}, []string{"hostname"}); value != "" {
			addresses = append(addresses, value)
		}
	}
	return strings.Join(addresses, ",")
}

func labelMap(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func hpaTargets(obj map[string]any) string {
	current := firstInt(obj, []string{"status", "currentReplicas"})
	desired := firstInt(obj, []string{"status", "desiredReplicas"})
	if current != "" || desired != "" {
		return current + "/" + desired
	}
	return ""
}

func durationBetween(obj map[string]any, startPath, endPath []string) string {
	start := timestampString(obj, startPath...)
	end := timestampString(obj, endPath...)
	if start == "" || end == "" {
		return ""
	}
	startTime, startErr := time.Parse(time.RFC3339, start)
	endTime, endErr := time.Parse(time.RFC3339, end)
	if startErr != nil || endErr != nil || endTime.Before(startTime) {
		return ""
	}
	return endTime.Sub(startTime).Round(time.Second).String()
}

func timestampString(obj map[string]any, path ...string) string {
	if value := nestedString(obj, path...); value != "" {
		return value
	}
	if value, ok, _ := unstructured.NestedFieldNoCopy(obj, path...); ok {
		return fmt.Sprint(value)
	}
	return ""
}

func conditionStatus(obj map[string]any, conditionType string) string {
	conditions, ok, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !ok {
		return ""
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || nestedString(condition, "type") != conditionType {
			continue
		}
		return nestedString(condition, "status")
	}
	return ""
}

func stringSlice(obj map[string]any, path ...string) string {
	items, ok, _ := unstructured.NestedStringSlice(obj, path...)
	if !ok {
		return ""
	}
	return strings.Join(items, ",")
}

func (g GenericRenderer) valueFor(u *unstructured.Unstructured, col string) string {
	if path := g.Paths[strings.ToUpper(col)]; path != "" {
		return jsonPathValue(u.Object, path)
	}
	return field(u.Object, col)
}

func jsonPathValue(obj map[string]any, path string) string {
	path = strings.TrimPrefix(path, ".")
	path = strings.TrimPrefix(path, "{.")
	path = strings.TrimSuffix(path, "}")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	var current any = obj
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return fmt.Sprint(current)
		}
		current, ok = m[part]
		if !ok {
			return ""
		}
	}
	switch typed := current.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
