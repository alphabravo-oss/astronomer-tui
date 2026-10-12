package ui

import (
	"context"
	"sort"
	"strings"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type podReference struct{ kind, name string }

func podReferences(obj *unstructured.Unstructured) []podReference {
	var pod corev1.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod); err != nil {
		return nil
	}
	refs := map[podReference]bool{}
	add := func(kind, name string) {
		if name != "" {
			refs[podReference{kind, name}] = true
		}
	}
	account := pod.Spec.ServiceAccountName
	if account == "" {
		account = "default"
	}
	add("ServiceAccount", account)
	add("PriorityClass", pod.Spec.PriorityClassName)
	add("Node", pod.Spec.NodeName)
	for _, secret := range pod.Spec.ImagePullSecrets {
		add("Secret", secret.Name)
	}
	for _, v := range pod.Spec.Volumes {
		if v.Secret != nil {
			add("Secret", v.Secret.SecretName)
		}
		if v.ConfigMap != nil {
			add("ConfigMap", v.ConfigMap.Name)
		}
		if v.PersistentVolumeClaim != nil {
			add("PersistentVolumeClaim", v.PersistentVolumeClaim.ClaimName)
		}
		if v.Projected != nil {
			for _, source := range v.Projected.Sources {
				if source.Secret != nil {
					add("Secret", source.Secret.Name)
				}
				if source.ConfigMap != nil {
					add("ConfigMap", source.ConfigMap.Name)
				}
			}
		}
	}
	containers := append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...)
	for _, e := range pod.Spec.EphemeralContainers {
		containers = append(containers, corev1.Container{Env: e.Env, EnvFrom: e.EnvFrom})
	}
	for _, c := range containers {
		for _, e := range c.Env {
			if e.ValueFrom != nil {
				if e.ValueFrom.SecretKeyRef != nil {
					add("Secret", e.ValueFrom.SecretKeyRef.Name)
				}
				if e.ValueFrom.ConfigMapKeyRef != nil {
					add("ConfigMap", e.ValueFrom.ConfigMapKeyRef.Name)
				}
			}
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef != nil {
				add("Secret", e.SecretRef.Name)
			}
			if e.ConfigMapRef != nil {
				add("ConfigMap", e.ConfigMapRef.Name)
			}
		}
	}
	out := make([]podReference, 0, len(refs))
	for r := range refs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].kind+out[i].name < out[j].kind+out[j].name })
	return out
}
func (a *App) showReferences() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	if obj.GetKind() == "Pod" {
		refs := podReferences(obj)
		list := tview.NewList().ShowSecondaryText(false)
		list.SetBorder(true).SetTitle(" Pod references · Enter browse · Esc back ")
		for _, ref := range refs {
			ref := ref
			list.AddItem(tview.Escape(ref.kind+"/"+ref.name), "", 0, func() {
				g, err := a.resolveResource(ref.kind)
				if err != nil {
					a.showMessage("Reference", err.Error())
					return
				}
				ns := obj.GetNamespace()
				if ref.kind == "Node" || ref.kind == "PriorityClass" {
					ns = ""
				}
				a.relatedView(g, ns, func(u *unstructured.Unstructured) bool { return u.GetName() == ref.name })
			})
		}
		list.SetInputCapture(a.backCapture())
		a.app.SetRoot(list, true).SetFocus(list)
		return
	}
	ns := obj.GetNamespace()
	a.relatedView(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, ns, func(p *unstructured.Unstructured) bool {
		for _, ref := range podReferences(p) {
			if ref.kind == obj.GetKind() && ref.name == obj.GetName() {
				return true
			}
		}
		return false
	})
}
func (a *App) showWorkloads() {
	client := a.options.Client
	ns := client.Namespace
	a.operation("Load workloads", func(ctx context.Context) (func(), error) {
		var objects []cluster.GraphObject
		var errors []string
		for _, g := range []schema.GroupVersionResource{{Group: "apps", Version: "v1", Resource: "deployments"}, {Group: "apps", Version: "v1", Resource: "statefulsets"}, {Group: "apps", Version: "v1", Resource: "daemonsets"}, {Group: "apps", Version: "v1", Resource: "replicasets"}, {Group: "batch", Version: "v1", Resource: "jobs"}, {Group: "batch", Version: "v1", Resource: "cronjobs"}} {
			list, err := client.Dynamic.Resource(g).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				errors = append(errors, g.Resource+": "+err.Error())
				continue
			}
			for i := range list.Items {
				objects = append(objects, cluster.GraphObject{GVR: g, Object: list.Items[i].DeepCopy()})
			}
		}
		sort.Slice(objects, func(i, j int) bool {
			return objects[i].Object.GetNamespace()+objects[i].Object.GetName() < objects[j].Object.GetNamespace()+objects[j].Object.GetName()
		})
		return func() {
			table := a.explorerTable("Workloads · Enter browse · Esc back", []string{"NAMESPACE", "KIND", "NAME", "HEALTH"})
			for i, o := range objects {
				a.setExplorerRow(table, i+1, o.Object.GetNamespace(), o.Object.GetKind(), o.Object.GetName(), cluster.ResourceHealth(o.Object))
			}
			if len(errors) > 0 {
				a.setExplorerRow(table, len(objects)+1, "INCOMPLETE", strings.Join(errors, "; "))
			}
			if len(objects) == 0 && len(errors) == 0 {
				a.setExplorerRow(table, 1, "No workloads")
			}
			table.SetSelectedFunc(func(row, col int) {
				if row > 0 && row <= len(objects) {
					o := objects[row-1]
					a.relatedView(o.GVR, o.Object.GetNamespace(), func(u *unstructured.Unstructured) bool { return u.GetUID() == o.Object.GetUID() })
				}
			})
			table.SetInputCapture(a.backCapture())
			a.app.SetRoot(table, true).SetFocus(table)
		}, nil
	})
}
