package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func (a *App) toggleMarked() {
	obj := a.selectedObject()
	if obj == nil || obj.GetUID() == "" {
		return
	}
	if a.marked == nil {
		a.marked = map[types.UID]*unstructured.Unstructured{}
	}
	if _, ok := a.marked[obj.GetUID()]; ok {
		delete(a.marked, obj.GetUID())
	} else {
		if len(a.marked) >= 100 {
			a.showMessage("Selection", "At most 100 resources may be marked per operation")
			return
		}
		a.marked[obj.GetUID()] = obj.DeepCopy()
	}
	a.renderCurrent()
}
func (a *App) deleteMarked(force bool) {
	var targets []*unstructured.Unstructured
	for _, obj := range a.marked {
		targets = append(targets, obj.DeepCopy())
	}
	if len(targets) == 0 {
		if obj := a.selectedObject(); obj != nil {
			targets = append(targets, obj.DeepCopy())
		}
	}
	if len(targets) == 0 {
		return
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].GetNamespace()+targets[i].GetName() < targets[j].GetNamespace()+targets[j].GetName()
	})
	name := "delete"
	if force {
		name = "force-delete"
	}
	client := a.options.Client
	g := a.currentGVR
	var names []string
	for _, obj := range targets {
		names = append(names, obj.GetNamespace()+"/"+obj.GetName())
	}
	description := fmt.Sprintf("%s %d selected resources in context %s?\n%s", name, len(targets), client.Context, strings.Join(names, "\n"))
	if force {
		description += "\nGrace period zero: workloads may still be running on an unreachable node."
	}
	a.confirmExternalMutation(name, description, func() {
		cfg := a.options.Config
		a.operationWithTimeout(name, 2*time.Minute, func(ctx context.Context) (func(), error) {
			executor := actions.Executor{Clientset: client.Clientset, Dynamic: client.Dynamic, Config: cfg, CurrentContext: client.Context}
			var result strings.Builder
			for _, obj := range targets {
				if ctx.Err() != nil {
					break
				}
				req := actionRequest(name, g, obj)
				req.ConfirmText = client.Context
				_, err := executor.Run(ctx, req)
				if err != nil {
					fmt.Fprintf(&result, "FAILED %s/%s: %v\n", obj.GetNamespace(), obj.GetName(), err)
				} else {
					fmt.Fprintf(&result, "Deleted %s/%s\n", obj.GetNamespace(), obj.GetName())
				}
			}
			return func() { a.marked = nil; a.showDocument("Delete results", result.String()) }, nil
		})
	})
}
