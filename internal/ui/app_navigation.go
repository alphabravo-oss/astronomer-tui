//revive:disable:exported package-comments
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

type navigationTarget struct {
	Title  string
	GVR    schema.GroupVersionResource
	Filter string
}

func (a *App) showDescribe() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	a.showScrollableText(" Describe: "+obj.GetKind()+"/"+obj.GetName()+" ", describeText(obj, a.renderer))
}

func describeText(obj *unstructured.Unstructured, renderer views.Renderer) string {
	lines := []string{
		"Name:        " + obj.GetName(),
		"Namespace:   " + obj.GetNamespace(),
		"Kind:        " + obj.GetKind(),
		"API Version: " + obj.GetAPIVersion(),
		"UID:         " + string(obj.GetUID()),
		"Created:     " + obj.GetCreationTimestamp().Format(time.RFC3339),
		"Labels:      " + selectorText(obj.GetLabels()),
	}
	if renderer != nil {
		headers, row := renderer.Header(), renderer.Row(obj)
		lines = append(lines, "", "Summary")
		for i := 0; i < len(headers) && i < len(row); i++ {
			lines = append(lines, fmt.Sprintf("%-20s %s", headers[i].Name+":", row[i].Text))
		}
	}
	if owners := obj.GetOwnerReferences(); len(owners) > 0 {
		lines = append(lines, "", "Owners")
		for _, owner := range owners {
			lines = append(lines, "- "+owner.Kind+"/"+owner.Name)
		}
	}
	if annotations := obj.GetAnnotations(); len(annotations) > 0 {
		keys := make([]string, 0, len(annotations))
		for key := range annotations {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines = append(lines, "", "Annotations")
		for _, key := range keys {
			lines = append(lines, key+"="+annotations[key])
		}
	}
	if conditions, ok, _ := unstructured.NestedSlice(obj.Object, "status", "conditions"); ok && len(conditions) > 0 {
		lines = append(lines, "", "Conditions")
		for _, condition := range conditions {
			data, _ := json.Marshal(condition)
			lines = append(lines, string(data))
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) showEventsForSelected() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	if client == nil || client.Clientset == nil {
		a.showMessage("Events unavailable", "Kubernetes clientset is required")
		return
	}
	ctx, cancel := context.WithTimeout(a.context(), 8*time.Second)
	defer cancel()
	selector := "involvedObject.name=" + obj.GetName()
	if obj.GetUID() != "" {
		selector = "involvedObject.uid=" + string(obj.GetUID())
	}
	list, err := client.Clientset.CoreV1().Events(obj.GetNamespace()).List(ctx, metav1.ListOptions{FieldSelector: selector, Limit: 250})
	if err != nil {
		a.showMessage("Events unavailable", err.Error())
		return
	}
	lines := make([]string, 0, len(list.Items))
	for _, event := range list.Items {
		stamp := event.LastTimestamp.Time
		if stamp.IsZero() {
			stamp = event.EventTime.Time
		}
		lines = append(lines, fmt.Sprintf("%s  %-7s  %-24s  %s", stamp.Format(time.RFC3339), event.Type, event.Reason, event.Message))
	}
	if len(lines) == 0 {
		lines = append(lines, "No events found for the selected resource.")
	}
	a.showScrollableText(" Events: "+obj.GetKind()+"/"+obj.GetName()+" ", strings.Join(lines, "\n"))
}

func (a *App) showOwnerForSelected() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	owner, ok := controllerOwner(obj.GetOwnerReferences())
	if !ok {
		a.showMessage("Owner", "Selected resource has no owner reference")
		return
	}
	gvr, ok := a.gvrForOwner(owner.APIVersion, owner.Kind)
	if !ok {
		a.showMessage("Owner unavailable", "Could not resolve "+owner.APIVersion+" "+owner.Kind)
		return
	}
	if a.switchResourceFiltered(a.context(), gvr, owner.Name) {
		a.app.SetRoot(a.table, true)
	}
}

func (a *App) showRelatedForSelected() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	targets := a.relatedTargets(obj)
	if len(targets) == 0 {
		a.showMessage("Related resources", "No related-resource navigation is available for "+obj.GetKind())
		return
	}
	list := tview.NewList().ShowSecondaryText(true)
	for _, target := range targets {
		target := target
		list.AddItem(target.Title, target.GVR.String(), 0, func() {
			if a.switchResourceFiltered(a.context(), target.GVR, target.Filter) {
				a.app.SetRoot(a.table, true)
			}
		})
	}
	list.SetTitle(" Related: " + obj.GetKind() + "/" + obj.GetName() + " ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) showResourceGraph() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	ctx, cancel := context.WithTimeout(a.context(), 8*time.Second)
	defer cancel()
	lines := []string{obj.GetKind() + "/" + obj.GetName()}
	current := obj
	for depth := 0; depth < 8; depth++ {
		owner, ok := controllerOwner(current.GetOwnerReferences())
		if !ok {
			break
		}
		lines = append(lines, strings.Repeat("  ", depth+1)+"└─ owner "+owner.Kind+"/"+owner.Name)
		if client == nil || client.Dynamic == nil {
			break
		}
		gvr, resolved := a.gvrForOwner(owner.APIVersion, owner.Kind)
		if !resolved {
			break
		}
		resource := client.Dynamic.Resource(gvr)
		var next *unstructured.Unstructured
		var err error
		if current.GetNamespace() != "" && a.resourceNamespaced(gvr) {
			next, err = resource.Namespace(current.GetNamespace()).Get(ctx, owner.Name, metav1.GetOptions{})
		} else {
			next, err = resource.Get(ctx, owner.Name, metav1.GetOptions{})
		}
		if err != nil {
			lines = append(lines, strings.Repeat("  ", depth+2)+"└─ lookup error: "+err.Error())
			break
		}
		current = next
	}
	for _, target := range a.relatedTargets(obj) {
		lines = append(lines, "  ├─ related "+target.Title+" ["+target.GVR.Resource+"]")
	}
	a.showScrollableText(" Resource graph ", strings.Join(lines, "\n"))
}

func (a *App) showScrollableText(title, body string) {
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetWrap(false).SetText(body)
	view.SetTitle(title).SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) relatedTargets(obj *unstructured.Unstructured) []navigationTarget {
	var targets []navigationTarget
	if owner, ok := controllerOwner(obj.GetOwnerReferences()); ok {
		if gvr, resolved := a.gvrForOwner(owner.APIVersion, owner.Kind); resolved {
			targets = append(targets, navigationTarget{Title: "Owner " + owner.Kind + "/" + owner.Name, GVR: gvr, Filter: owner.Name})
		}
	}
	selector := nestedStringMap(obj.Object, "spec", "selector", "matchLabels")
	if len(selector) == 0 && obj.GetKind() == "Service" {
		selector = nestedStringMap(obj.Object, "spec", "selector")
	}
	selectorFilter := selectorText(selector)
	switch obj.GetKind() {
	case "Deployment":
		targets = append(targets,
			navigationTarget{Title: "ReplicaSets", GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Filter: obj.GetName()},
			navigationTarget{Title: "Pods", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Filter: selectorFilter},
		)
	case "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		targets = append(targets, navigationTarget{Title: "Pods", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Filter: selectorFilter})
	case "CronJob":
		targets = append(targets, navigationTarget{Title: "Jobs", GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Filter: obj.GetName()})
	case "Service":
		targets = append(targets,
			navigationTarget{Title: "Endpoints", GVR: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}, Filter: obj.GetName()},
			navigationTarget{Title: "EndpointSlices", GVR: schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, Filter: obj.GetName()},
			navigationTarget{Title: "Pods", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Filter: selectorFilter},
		)
	case "PersistentVolumeClaim":
		if volume := nestedObjectString(obj.Object, "spec", "volumeName"); volume != "" {
			targets = append(targets, navigationTarget{Title: "PersistentVolume " + volume, GVR: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}, Filter: volume})
		}
	case "HorizontalPodAutoscaler":
		kind := nestedObjectString(obj.Object, "spec", "scaleTargetRef", "kind")
		name := nestedObjectString(obj.Object, "spec", "scaleTargetRef", "name")
		apiVersion := nestedObjectString(obj.Object, "spec", "scaleTargetRef", "apiVersion")
		if gvr, ok := a.gvrForOwner(apiVersion, kind); ok && name != "" {
			targets = append(targets, navigationTarget{Title: kind + " " + name, GVR: gvr, Filter: name})
		}
	}
	filtered := targets[:0]
	seen := map[string]struct{}{}
	for _, target := range targets {
		if target.Filter == "" {
			continue
		}
		key := target.GVR.String() + "/" + target.Filter
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, target)
	}
	return filtered
}

func (a *App) gvrForOwner(apiVersion, kind string) (schema.GroupVersionResource, bool) {
	gv, _ := schema.ParseGroupVersion(apiVersion)
	a.mu.RLock()
	defer a.mu.RUnlock()
	for gvr, info := range a.resourceInfo {
		if info.Kind == kind && (apiVersion == "" || (gvr.Group == gv.Group && gvr.Version == gv.Version)) {
			return gvr, true
		}
	}
	return ownerGVR(kind)
}

func controllerOwner(owners []metav1.OwnerReference) (metav1.OwnerReference, bool) {
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			return owner, true
		}
	}
	if len(owners) > 0 {
		return owners[0], true
	}
	return metav1.OwnerReference{}, false
}

func nestedStringMap(obj map[string]any, path ...string) map[string]string {
	raw, ok, _ := unstructured.NestedStringMap(obj, path...)
	if !ok {
		return nil
	}
	return raw
}

func nestedObjectString(obj map[string]any, path ...string) string {
	value, _, _ := unstructured.NestedString(obj, path...)
	return value
}

func selectorText(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ",")
}
