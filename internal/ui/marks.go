//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/safety"
)

const markGlyph = "●"

// toggleMark marks/unmarks the currently selected row.
func (a *App) toggleMark() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	key := objectKey(obj)
	a.mu.Lock()
	if a.marks == nil {
		a.marks = map[string]struct{}{}
	}
	if _, ok := a.marks[key]; ok {
		delete(a.marks, key)
	} else {
		a.marks[key] = struct{}{}
	}
	a.mu.Unlock()
	a.renderCurrent()
}

// markAllVisible marks every currently visible row.
func (a *App) markAllVisible() {
	a.mu.Lock()
	if a.marks == nil {
		a.marks = map[string]struct{}{}
	}
	for _, obj := range a.visibleObjects {
		a.marks[objectKey(obj)] = struct{}{}
	}
	a.mu.Unlock()
	a.renderCurrent()
}

// clearMarks removes all marks.
func (a *App) clearMarks() {
	a.mu.Lock()
	a.marks = nil
	a.mu.Unlock()
	a.renderCurrent()
}

func (a *App) isMarked(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.marks[key]
	return ok
}

// markedObjects returns the current objects that are marked, in table order.
func (a *App) markedObjects() []*unstructured.Unstructured {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.marks) == 0 {
		return nil
	}
	var out []*unstructured.Unstructured
	for _, obj := range a.visibleObjects {
		if _, ok := a.marks[objectKey(obj)]; ok {
			out = append(out, obj)
		}
	}
	return out
}

// showBulkDelete confirms and deletes all marked objects with a single typed
// confirmation covering the batch (prod-guard aware).
func (a *App) showBulkDelete(objs []*unstructured.Unstructured) {
	if a.renderer == nil {
		return
	}
	gvr := a.renderer.GVR()
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, objectKey(o))
	}
	ctxName := ""
	if a.options.Client != nil {
		ctxName = a.options.Client.Context
	}
	summary := fmt.Sprintf("Delete %d %s:\n%s", len(objs), gvr.Resource, strings.Join(names, "\n"))
	if safety.RequiresConfirmation(a.options.Config, ctxName, safety.Delete) {
		input := tview.NewInputField().
			SetLabel(fmt.Sprintf("Type %s to delete %d resources: ", ctxName, len(objs))).
			SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.runBulkDelete(gvr, objs, input.GetText())
				return
			}
			a.closeModal()
		})
		a.showModal(input)
		return
	}
	modal := tview.NewModal().
		SetText(summary).
		AddButtons([]string{"Delete", "Cancel"}).
		SetDoneFunc(func(index int, _ string) {
			if index == 0 {
				a.runBulkDelete(gvr, objs, "")
				return
			}
			a.closeModal()
		})
	a.showModal(modal)
}

// runBulkDelete authorizes once then deletes each object, flashing a summary.
func (a *App) runBulkDelete(gvr schema.GroupVersionResource, objs []*unstructured.Unstructured, confirm string) {
	ctxName := ""
	if a.options.Client != nil {
		ctxName = a.options.Client.Context
	}
	if err := safety.Authorize(a.options.Config, ctxName, safety.Delete, confirm); err != nil {
		a.showMessage("Delete failed", err.Error())
		return
	}
	a.closeModal()
	var deleted, failed int
	var lastErr string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, obj := range objs {
		if err := actions.DeleteResource(ctx, a.options.Client.Dynamic, gvr, obj.GetNamespace(), obj.GetName()); err != nil {
			failed++
			lastErr = err.Error()
			continue
		}
		deleted++
	}
	a.clearMarks()
	if failed > 0 {
		a.Flash(FlashError, "deleted %d/%d %s — last error: %s", deleted, len(objs), gvr.Resource, lastErr)
		return
	}
	a.Flash(FlashInfo, "deleted %d %s", deleted, gvr.Resource)
}
