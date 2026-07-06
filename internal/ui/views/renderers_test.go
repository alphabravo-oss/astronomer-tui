package views

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGenericRendererFallback(t *testing.T) {
	r := GenericRenderer{Resource: schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}}
	obj := &unstructured.Unstructured{}
	obj.SetName("one")
	obj.SetNamespace("default")
	obj.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-2 * time.Hour)))
	row := r.Row(obj)
	if row[0].Text != "one" || row[1].Text != "default" || row[2].Text == "" {
		t.Fatalf("unexpected row %#v", row)
	}
}

func TestDetailRendersYAML(t *testing.T) {
	r := GenericRenderer{Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name": "settings",
		},
		"data": map[string]any{"mode": "prod"},
	}}
	obj.SetName("settings")
	detail := r.Detail(obj)
	if detail.Title != "ConfigMap/settings" || !strings.Contains(detail.Body, "mode: prod") {
		t.Fatalf("unexpected detail %#v", detail)
	}
}

func TestRendererForCRDPrinterColumns(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "widgets",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:   "v1",
				Served: true,
				AdditionalPrinterColumns: []apiextensionsv1.CustomResourceColumnDefinition{{
					Name:     "Phase",
					JSONPath: ".status.phase",
				}},
			}},
		},
	}
	renderer := RendererForCRD(crd, "v1")
	obj := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "one"},
		"status":   map[string]any{"phase": "Ready"},
	}}
	obj.SetName("one")
	headers := renderer.Header()
	row := renderer.Row(obj)
	if len(headers) != 2 || headers[1].Name != "PHASE" || row[1].Text != "Ready" {
		t.Fatalf("headers=%#v row=%#v", headers, row)
	}
}

func TestRendererForCRDWithoutPrinterColumnsFallsBackToNameAge(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "widgets",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:   "v1",
				Served: true,
			}},
		},
	}
	renderer := RendererForCRD(crd, "v1")
	headers := renderer.Header()
	if len(headers) != 2 || headers[0].Name != "NAME" || headers[1].Name != "AGE" {
		t.Fatalf("unexpected fallback headers %#v", headers)
	}
}

func TestBuiltinRendererHeadersForBreadthResources(t *testing.T) {
	for gvr, want := range map[schema.GroupVersionResource][]string{
		{Group: "apps", Version: "v1", Resource: "daemonsets"}:                        {"NAMESPACE", "NAME", "DESIRED", "CURRENT", "READY", "UP-TO-DATE", "AVAILABLE", "LABELS", "AGE"},
		{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}:   {"NAME", "NAMESPACE", "REFERENCE", "TARGETS", "MINPODS", "MAXPODS", "REPLICAS", "AGE"},
		{Version: "v1", Resource: "persistentvolumes"}:                                {"NAME", "CAPACITY", "ACCESS MODES", "RECLAIM POLICY", "STATUS", "CLAIM", "STORAGECLASS", "REASON", "VOLUMEMODE", "LABELS", "AGE"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}: {"NAME", "LABELS", "AGE"},
		{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}:     {"NAME", "SERVICE", "AVAILABLE", "AGE"},
	} {
		renderer, ok := Builtins()[gvr]
		if !ok {
			t.Fatalf("missing renderer for %#v", gvr)
		}
		var got []string
		for _, header := range renderer.Header() {
			got = append(got, header.Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s headers got %#v want %#v", gvr, got, want)
		}
	}
}

func TestBuiltinRendererExtractsCommonFields(t *testing.T) {
	renderer := Builtins()[schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}]
	obj := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "agent", "namespace": "default"},
		"status": map[string]any{
			"desiredNumberScheduled": int64(3),
			"currentNumberScheduled": int64(2),
			"readyReplicas":          int64(1),
			"replicas":               int64(3),
		},
	}}
	row := renderer.Row(obj)
	got := []string{
		cellText(renderer, row, "DESIRED"),
		cellText(renderer, row, "CURRENT"),
		cellText(renderer, row, "READY"),
	}
	if !reflect.DeepEqual(got, []string{"3", "2", "1/3"}) {
		t.Fatalf("unexpected daemonset row %#v", row)
	}
	pvRenderer := Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}]
	pv := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "pv-a"},
		"spec": map[string]any{
			"capacity":                      map[string]any{"storage": "10Gi"},
			"accessModes":                   []any{"ReadWriteOnce"},
			"persistentVolumeReclaimPolicy": "Delete",
			"claimRef":                      map[string]any{"namespace": "default", "name": "data"},
		},
	}}
	pvRow := pvRenderer.Row(pv)
	if cellText(pvRenderer, pvRow, "CAPACITY") != "10Gi" || cellText(pvRenderer, pvRow, "ACCESS MODES") != "ReadWriteOnce" || cellText(pvRenderer, pvRow, "CLAIM") != "default/data" {
		t.Fatalf("unexpected pv row %#v", pvRow)
	}
}

func TestBuiltinRendererExtractsCoreNetworkingAndClusterFields(t *testing.T) {
	podRenderer := Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "pods"}]
	pod := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "api", "namespace": "default"},
		"status": map[string]any{
			"phase": "Running",
			"containerStatuses": []any{
				map[string]any{"ready": true, "restartCount": int64(2)},
				map[string]any{"ready": false, "restartCount": int64(1)},
			},
		},
	}}
	podRow := podRenderer.Row(pod)
	if cellText(podRenderer, podRow, "STATUS") != "Running" || cellText(podRenderer, podRow, "READY") != "1/2" || cellText(podRenderer, podRow, "RESTARTS") != "3" {
		t.Fatalf("unexpected pod row %#v", podRow)
	}

	serviceRenderer := Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "services"}]
	service := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "api", "namespace": "default"},
		"spec": map[string]any{
			"type":      "ClusterIP",
			"clusterIP": "10.0.0.1",
			"ports":     []any{map[string]any{"port": int64(80), "protocol": "TCP"}},
		},
	}}
	serviceRow := serviceRenderer.Row(service)
	if cellText(serviceRenderer, serviceRow, "TYPE") != "ClusterIP" || cellText(serviceRenderer, serviceRow, "CLUSTER-IP") != "10.0.0.1" || cellText(serviceRenderer, serviceRow, "PORTS") != "80/TCP" {
		t.Fatalf("unexpected service row %#v", serviceRow)
	}

	nodeRenderer := Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "nodes"}]
	node := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "worker", "labels": map[string]any{"node-role.kubernetes.io/worker": ""}},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			"nodeInfo":   map[string]any{"kubeletVersion": "v1.30.0"},
		},
	}}
	nodeRow := nodeRenderer.Row(node)
	if cellText(nodeRenderer, nodeRow, "STATUS") != "Ready" || cellText(nodeRenderer, nodeRow, "ROLE") != "worker" || cellText(nodeRenderer, nodeRow, "VERSION") != "v1.30.0" {
		t.Fatalf("unexpected node row %#v", nodeRow)
	}

	eventRenderer := Builtins()[schema.GroupVersionResource{Version: "v1", Resource: "events"}]
	event := &unstructured.Unstructured{Object: map[string]any{
		"lastTimestamp": "2026-05-09T00:00:00Z",
		"type":          "Warning",
		"reason":        "Failed",
		"message":       "pull failed",
		"involvedObject": map[string]any{
			"kind": "Pod",
			"name": "api",
		},
	}}
	eventRow := eventRenderer.Row(event)
	if eventRow[0].Text == "" || eventRow[1].Text != "Warning" || eventRow[3].Text != "Pod/api" || eventRow[4].Text != "pull failed" {
		t.Fatalf("unexpected event row %#v", eventRow)
	}
}

func TestPodStatusDerivation(t *testing.T) {
	base := func(mutate func(obj map[string]any)) map[string]any {
		obj := map[string]any{
			"metadata": map[string]any{"name": "api"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "app"}}},
			"status":   map[string]any{"phase": "Running"},
		}
		mutate(obj)
		return obj
	}
	cases := []struct {
		name   string
		obj    map[string]any
		expect string
	}{
		{"running", base(func(map[string]any) {}), "Running"},
		{"terminating", base(func(obj map[string]any) {
			obj["metadata"].(map[string]any)["deletionTimestamp"] = "2026-05-09T00:00:00Z"
		}), "Terminating"},
		{"completed", base(func(obj map[string]any) {
			obj["status"].(map[string]any)["phase"] = "Succeeded"
		}), "Completed"},
		{"crashloop", base(func(obj map[string]any) {
			obj["status"].(map[string]any)["containerStatuses"] = []any{
				map[string]any{"state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}},
			}
		}), "CrashLoopBackOff"},
		{"init failure", base(func(obj map[string]any) {
			obj["status"].(map[string]any)["initContainerStatuses"] = []any{
				map[string]any{"state": map[string]any{"waiting": map[string]any{"reason": "ImagePullBackOff"}}},
			}
		}), "Init:ImagePullBackOff"},
	}
	for _, tc := range cases {
		if got := podStatus(tc.obj); got != tc.expect {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.expect)
		}
	}
}

func TestBuiltinRendererExtractsBreadthFields(t *testing.T) {
	ingressRenderer := Builtins()[schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}]
	ingress := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "web", "namespace": "default"},
		"spec": map[string]any{
			"ingressClassName": "nginx",
			"rules":            []any{map[string]any{"host": "app.example.com"}},
		},
		"status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": "1.2.3.4"}}}},
	}}
	ingressRow := ingressRenderer.Row(ingress)
	if ingressRow[2].Text != "nginx" || ingressRow[3].Text != "app.example.com" || ingressRow[4].Text != "1.2.3.4" {
		t.Fatalf("unexpected ingress row %#v", ingressRow)
	}

	scRenderer := Builtins()[schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}]
	sc := &unstructured.Unstructured{Object: map[string]any{
		"metadata":             map[string]any{"name": "fast"},
		"provisioner":          "kubernetes.io/no-provisioner",
		"reclaimPolicy":        "Delete",
		"volumeBindingMode":    "WaitForFirstConsumer",
		"allowVolumeExpansion": true,
	}}
	scRow := scRenderer.Row(sc)
	if scRow[1].Text != "kubernetes.io/no-provisioner" || scRow[3].Text != "WaitForFirstConsumer" || scRow[4].Text != "true" {
		t.Fatalf("unexpected storageclass row %#v", scRow)
	}
}

func TestBuiltinRendererSnapshot(t *testing.T) {
	renderers := Builtins()
	gvrs := make([]schema.GroupVersionResource, 0, len(renderers))
	for gvr := range renderers {
		gvrs = append(gvrs, gvr)
	}
	sort.Slice(gvrs, func(i, j int) bool {
		return gvrs[i].String() < gvrs[j].String()
	})
	var out strings.Builder
	for _, gvr := range gvrs {
		renderer := renderers[gvr]
		fmt.Fprintf(&out, "%s\n", gvr.String())
		fmt.Fprintf(&out, "  headers: %s\n", strings.Join(headerNames(renderer.Header()), " | "))
		fmt.Fprintf(&out, "  row: %s\n", strings.Join(cellTexts(renderer.Row(rendererSnapshotObject(gvr))), " | "))
	}
	if os.Getenv("ASTROTUI_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile("testdata/builtin_renderers.golden", []byte(out.String()), 0o600); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile("testdata/builtin_renderers.golden") // #nosec G304 -- test reads local golden snapshot.
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := out.String(); got != string(want) {
		t.Fatalf("renderer snapshot mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFilterRows(t *testing.T) {
	rows := [][]Cell{{{Text: "api"}}, {{Text: "worker"}}}
	got := FilterRows(rows, "api")
	if len(got) != 1 || got[0][0].Text != "api" {
		t.Fatalf("unexpected rows %#v", got)
	}
}

func headerNames(headers []Column) []string {
	out := make([]string, 0, len(headers))
	for _, header := range headers {
		out = append(out, header.Name)
	}
	return out
}

func cellTexts(cells []Cell) []string {
	out := make([]string, 0, len(cells))
	for _, cell := range cells {
		out = append(out, cell.Text)
	}
	return out
}

func rendererSnapshotObject(gvr schema.GroupVersionResource) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GroupVersion().String(),
		"kind":       "Snapshot",
		"type":       "Opaque",
		"metadata": map[string]any{
			"name":      "sample",
			"namespace": "default",
			"labels": map[string]any{
				"app":                            "api",
				"node-role.kubernetes.io/worker": "",
			},
		},
		"data":                 map[string]any{"key": "value"},
		"provisioner":          "kubernetes.io/no-provisioner",
		"reclaimPolicy":        "Delete",
		"volumeBindingMode":    "WaitForFirstConsumer",
		"allowVolumeExpansion": true,
		"addressType":          "IPv4",
		"lastTimestamp":        "2026-05-09T00:00:00Z",
		"eventTime":            "2026-05-09T00:00:00Z",
		"reason":               "Failed",
		"message":              "pull failed",
		"involvedObject": map[string]any{
			"kind": "Pod",
			"name": "sample",
		},
		"roleRef": map[string]any{
			"kind": "ClusterRole",
			"name": "admin",
		},
		"subjects": []any{map[string]any{
			"kind": "ServiceAccount",
			"name": "default",
		}},
		"secrets": []any{map[string]any{"name": "token"}},
		"endpoints": []any{
			map[string]any{"addresses": []any{"10.0.0.1"}},
			map[string]any{"addresses": []any{"10.0.0.2"}},
		},
		"subsets": []any{map[string]any{"addresses": []any{map[string]any{"ip": "10.0.0.1"}}}},
		"ports":   []any{map[string]any{"port": int64(80), "protocol": "TCP"}},
		"spec": map[string]any{
			"type":                          "ClusterIP",
			"clusterIP":                     "10.0.0.1",
			"replicas":                      int64(3),
			"completions":                   int64(1),
			"schedule":                      "*/5 * * * *",
			"suspend":                       false,
			"minReplicas":                   int64(1),
			"maxReplicas":                   int64(5),
			"capacity":                      map[string]any{"storage": "10Gi"},
			"accessModes":                   []any{"ReadWriteOnce"},
			"persistentVolumeReclaimPolicy": "Delete",
			"claimRef":                      map[string]any{"namespace": "default", "name": "data"},
			"volumeName":                    "pv-a",
			"provisioner":                   "kubernetes.io/no-provisioner",
			"ingressClassName":              "nginx",
			"attacher":                      "csi.example.com",
			"nodeName":                      "node-a",
			"holderIdentity":                "holder-a",
			"source":                        map[string]any{"persistentVolumeName": "pv-a"},
			"service":                       map[string]any{"namespace": "default", "name": "api"},
			"scaleTargetRef":                map[string]any{"kind": "Deployment", "name": "api"},
			"podSelector":                   map[string]any{"matchLabels": map[string]any{"app": "api"}},
			"minAvailable":                  int64(1),
			"maxUnavailable":                int64(1),
			"ports":                         []any{map[string]any{"port": int64(80), "protocol": "TCP"}},
			"rules":                         []any{map[string]any{"host": "app.example.com"}},
		},
		"status": map[string]any{
			"phase":                  "Running",
			"replicas":               int64(3),
			"readyReplicas":          int64(2),
			"availableReplicas":      int64(2),
			"updatedReplicas":        int64(3),
			"desiredNumberScheduled": int64(3),
			"currentNumberScheduled": int64(3),
			"succeeded":              int64(1),
			"currentReplicas":        int64(2),
			"desiredReplicas":        int64(3),
			"attached":               true,
			"disruptionsAllowed":     int64(1),
			"expectedPods":           int64(3),
			"lastScheduleTime":       "2026-05-09T00:00:00Z",
			"startTime":              "2026-05-09T00:00:00Z",
			"completionTime":         "2026-05-09T00:01:00Z",
			"capacity":               map[string]any{"storage": "10Gi"},
			"active":                 []any{map[string]any{"name": "job"}},
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "Available", "status": "True"},
			},
			"containerStatuses": []any{
				map[string]any{"ready": true, "restartCount": int64(1)},
				map[string]any{"ready": false, "restartCount": int64(2)},
			},
			"nodeInfo":     map[string]any{"kubeletVersion": "v1.30.0"},
			"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": "1.2.3.4"}}},
		},
	}}
	obj.SetName("sample")
	if !clusterScopedSnapshotResource(gvr.Resource) {
		obj.SetNamespace("default")
	}
	switch gvr.Resource {
	case "apiservices":
		status := obj.Object["status"].(map[string]any)
		delete(status, "availableReplicas")
	case "nodes":
		delete(obj.Object, "roleRef")
		delete(obj.Object, "subjects")
		status := obj.Object["status"].(map[string]any)
		status["nodeInfo"] = map[string]any{
			"kubeletVersion": "v1.30.0",
			"architecture":   "amd64",
			"osImage":        "Ubuntu 24.04",
			"kernelVersion":  "6.8.0",
		}
		status["addresses"] = []any{
			map[string]any{"type": "InternalIP", "address": "10.0.0.10"},
			map[string]any{"type": "ExternalIP", "address": "203.0.113.10"},
		}
		spec := obj.Object["spec"].(map[string]any)
		spec["taints"] = []any{map[string]any{"key": "dedicated", "effect": "NoSchedule"}}
	case "pods":
		status := obj.Object["status"].(map[string]any)
		delete(status, "nodeInfo")
		status["podIP"] = "10.1.2.3"
		status["qosClass"] = "Burstable"
		status["containerStatuses"] = []any{
			map[string]any{
				"ready":        true,
				"restartCount": int64(1),
				"state":        map[string]any{"running": map[string]any{}},
			},
			map[string]any{
				"ready":        false,
				"restartCount": int64(2),
				"state":        map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}},
				"lastState":    map[string]any{"terminated": map[string]any{"finishedAt": "2026-05-09T00:00:00Z", "reason": "Error"}},
			},
		}
		spec := obj.Object["spec"].(map[string]any)
		spec["containers"] = []any{map[string]any{"name": "app", "image": "example/app:1"}}
		spec["serviceAccountName"] = "runner"
	case "events":
		delete(obj.Object, "spec")
		obj.Object["type"] = "Warning"
	case "secrets":
		delete(obj.Object, "spec")
		obj.Object["type"] = "Opaque"
	}
	return obj
}

func cellText(renderer Renderer, row []Cell, column string) string {
	for i, header := range renderer.Header() {
		if header.Name == column && i < len(row) {
			return row[i].Text
		}
	}
	return ""
}

func clusterScopedSnapshotResource(resource string) bool {
	switch resource {
	case "apiservices", "clusterroles", "clusterrolebindings", "namespaces", "nodes", "persistentvolumes", "storageclasses", "volumeattachments":
		return true
	default:
		return false
	}
}
