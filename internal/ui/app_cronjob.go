//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/safety"
)

// triggerStamp returns a unix timestamp suffix for manually triggered jobs.
func triggerStamp() int64 { return time.Now().Unix() }

// showTrigger creates a manual Job from the selected CronJob (k9s `t`).
func (a *App) showTrigger() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	if obj.GetKind() != "CronJob" {
		a.Flash(FlashInfo, "trigger is only available for cronjobs")
		return
	}
	name := obj.GetName()
	ns := obj.GetNamespace()
	jobName := fmt.Sprintf("%s-manual-%d", name, triggerStamp())
	run := func() {
		if a.options.Client == nil || a.options.Client.Clientset == nil {
			a.Flash(FlashError, "kubernetes clientset is required to trigger jobs")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := actions.TriggerCronJob(ctx, a.options.Client.Clientset, ns, name, jobName); err != nil {
			a.Flash(FlashError, "trigger failed: %s", err.Error())
			return
		}
		a.Flash(FlashInfo, "created job %s from cronjob/%s", jobName, name)
	}
	if safety.RequiresConfirmation(a.options.Config, a.clientContext(), safety.Restart) {
		a.confirmTyped(fmt.Sprintf("trigger %s", name), run)
		return
	}
	run()
}

// showSuspendToggle flips a CronJob's suspend flag (k9s `z`).
func (a *App) showSuspendToggle() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	if obj.GetKind() != "CronJob" {
		a.Flash(FlashInfo, "suspend is only available for cronjobs")
		return
	}
	name := obj.GetName()
	ns := obj.GetNamespace()
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
	target := !suspended
	run := func() {
		if a.options.Client == nil || a.options.Client.Clientset == nil {
			a.Flash(FlashError, "kubernetes clientset is required to suspend cronjobs")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := actions.SuspendCronJob(ctx, a.options.Client.Clientset, ns, name, target); err != nil {
			a.Flash(FlashError, "suspend toggle failed: %s", err.Error())
			return
		}
		state := "resumed"
		if target {
			state = "suspended"
		}
		a.Flash(FlashInfo, "%s cronjob/%s", state, name)
	}
	if safety.RequiresConfirmation(a.options.Config, a.clientContext(), safety.Edit) {
		a.confirmTyped(fmt.Sprintf("toggle suspend on %s", name), run)
		return
	}
	run()
}

func (a *App) clientContext() string {
	if a.options.Client != nil {
		return a.options.Client.Context
	}
	return ""
}

// confirmTyped shows a simple Run/Cancel confirmation before a mutating action
// in a guarded context.
func (a *App) confirmTyped(prompt string, run func()) {
	modal := tview.NewModal().
		SetText("Confirm: " + prompt + "?").
		AddButtons([]string{"Run", "Cancel"}).
		SetDoneFunc(func(index int, _ string) {
			a.closeModal()
			if index == 0 {
				run()
			}
		})
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey { return event })
	a.showModal(modal)
}
