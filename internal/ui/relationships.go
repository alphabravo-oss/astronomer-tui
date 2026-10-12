package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"strings"
)

type navigationState struct {
	gvr               schema.GroupVersionResource
	namespace, filter string
	predicate         func(*unstructured.Unstructured) bool
}

func (a *App) relatedView(g schema.GroupVersionResource, ns string, predicate func(*unstructured.Unstructured) bool) {
	a.history = append(a.history, navigationState{a.currentGVR, a.options.Client.Namespace, a.filter, a.relatedPredicate})
	a.options.Client.Namespace = ns
	a.switchResource(a.backgroundContext(), g)
	a.relatedPredicate = predicate
	a.reloadCurrent()
	a.showMain()
}
func (a *App) goBack() {
	if len(a.history) == 0 {
		return
	}
	s := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	a.options.Client.Namespace = s.namespace
	a.switchResource(a.backgroundContext(), s.gvr)
	a.filter = s.filter
	a.relatedPredicate = s.predicate
	a.reloadCurrent()
	a.showMain()
}
func (a *App) showRelationships() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	list := tview.NewList().ShowSecondaryText(true)
	list.SetTitle(" Relationships · " + tview.Escape(obj.GetKind()+"/"+obj.GetName()) + " ").SetBorder(true)
	add := func(title, description string, fn func()) { list.AddItem(title, description, 0, fn) }
	add("References", "ConfigMaps, Secrets, storage and pod consumers", a.showReferences)
	add("Events", "Exact object UID, including warnings", func() { a.showObjectEvents(obj) })
	for _, owner := range obj.GetOwnerReferences() {
		owner := owner
		add("Owner: "+tview.Escape(owner.Kind+"/"+owner.Name), "Verify identity before opening", func() { a.openOwner(obj, owner) })
	}
	switch obj.GetKind() {
	case "Pod":
		add("Container inspection", "Readiness, restarts, images, environment and shell", a.showContainers)
		var pod corev1.Pod
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod)
		add("Containers / logs", "Choose regular, init, ephemeral or all containers", func() { a.showLogContainerPicker(pod) })
	case "Node":
		add("Pods on node", obj.GetName(), func() {
			a.relatedView(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "", func(p *unstructured.Unstructured) bool {
				v, _, _ := unstructured.NestedString(p.Object, "spec", "nodeName")
				return v == obj.GetName()
			})
		})
	case "Service":
		selector, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "selector")
		if len(selector) > 0 {
			add("Selected pods", labels.Set(selector).String(), func() {
				s := labels.SelectorFromSet(selector)
				a.relatedView(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, obj.GetNamespace(), func(p *unstructured.Unstructured) bool { return s.Matches(labels.Set(p.GetLabels())) })
			})
		}
		add("EndpointSlices", "Service backends and readiness", func() {
			a.relatedView(schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, obj.GetNamespace(), func(p *unstructured.Unstructured) bool {
				return p.GetLabels()["kubernetes.io/service-name"] == obj.GetName()
			})
		})
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		raw, _, _ := unstructured.NestedMap(obj.Object, "spec", "selector")
		var selector metav1.LabelSelector
		if runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &selector) == nil {
			s, err := metav1.LabelSelectorAsSelector(&selector)
			if err == nil && !s.Empty() {
				add("Pods", s.String(), func() {
					a.relatedView(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, obj.GetNamespace(), func(p *unstructured.Unstructured) bool { return s.Matches(labels.Set(p.GetLabels())) })
				})
			}
		}
	case "CronJob":
		add("Jobs", "Owned by this CronJob UID", func() {
			a.relatedView(schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, obj.GetNamespace(), func(p *unstructured.Unstructured) bool {
				for _, o := range p.GetOwnerReferences() {
					if o.UID == obj.GetUID() {
						return true
					}
				}
				return false
			})
		})
	}
	add("YAML", "Resource details", func() { a.showDetailFor(obj) })
	add("Back", "Return to resource view", a.showMain)
	list.SetInputCapture(a.backCapture())
	a.app.SetRoot(list, true)
}
func (a *App) showObjectEvents(obj *unstructured.Unstructured) {
	uid := string(obj.GetUID())
	if uid == "" {
		a.showMessage("Events unavailable", "Object has no UID")
		return
	}
	a.relatedView(schema.GroupVersionResource{Version: "v1", Resource: "events"}, obj.GetNamespace(), func(e *unstructured.Unstructured) bool {
		v, _, _ := unstructured.NestedString(e.Object, "involvedObject", "uid")
		return v == uid
	})
}
func (a *App) openOwner(obj *unstructured.Unstructured, owner metav1.OwnerReference) {
	client := a.options.Client
	g, ok := ownerGVR(owner.Kind)
	if a.catalog != nil {
		for _, r := range a.catalog.Resources {
			gv := r.GVR.GroupVersion().String()
			if r.Kind == owner.Kind && gv == owner.APIVersion {
				g = r.GVR
				ok = true
				break
			}
		}
	}
	if !ok {
		a.showMessage("Owner unavailable", "Discover "+owner.APIVersion+"/"+owner.Kind+" with :refresh")
		return
	}
	ns := obj.GetNamespace()
	if r, found := a.catalog.Lookup(g); found && !r.Namespaced {
		ns = ""
	}
	a.operation("Open owner", func(ctx context.Context) (func(), error) {
		resource := client.Dynamic.Resource(g)
		var target *unstructured.Unstructured
		var err error
		if ns == "" {
			target, err = resource.Get(ctx, owner.Name, metav1.GetOptions{})
		} else {
			target, err = resource.Namespace(ns).Get(ctx, owner.Name, metav1.GetOptions{})
		}
		if err != nil {
			return nil, err
		}
		if target.GetUID() != owner.UID {
			return nil, fmt.Errorf("owner was replaced (UID mismatch)")
		}
		return func() {
			a.relatedView(g, ns, func(u *unstructured.Unstructured) bool { return u.GetUID() == owner.UID })
		}, nil
	})
}

// Description used in help and aliases without tying navigation to pluralization.
func resourceIdentity(g schema.GroupVersionResource) string {
	return strings.TrimPrefix(cluster.Qualified(g), "/")
}

func (a *App) backCapture() func(*tcell.EventKey) *tcell.EventKey {
	return func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		return e
	}
}
