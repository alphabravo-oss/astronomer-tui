//revive:disable:exported package-comments
package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

// promptField builds a labeled input that calls onEnter with the text on Enter
// and onCancel otherwise.
func promptField(label string, onEnter func(string), onCancel func()) *tview.InputField {
	input := tview.NewInputField().SetLabel(label).SetFieldWidth(40)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			onEnter(input.GetText())
			return
		}
		onCancel()
	})
	return input
}

// confirmModal shows a Run/Cancel confirmation displaying text.
func (a *App) confirmModal(text string, run func()) {
	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"Run", "Cancel"}).
		SetDoneFunc(func(index int, _ string) {
			a.closeModal()
			if index == 0 {
				run()
			}
		})
	a.showModal(modal)
}

// showKill deletes the selected (or marked) resources with grace period 0.
func (a *App) showKill() {
	if a.renderer == nil {
		return
	}
	objs := a.markedObjects()
	if len(objs) == 0 {
		if obj := a.selectedObject(); obj != nil {
			objs = []*unstructured.Unstructured{obj}
		}
	}
	if len(objs) == 0 {
		return
	}
	gvr := a.renderer.GVR()
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, objectKey(o))
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var killed, failed int
		var lastErr string
		for _, o := range objs {
			if err := actions.KillResource(ctx, a.options.Client.Dynamic, gvr, o.GetNamespace(), o.GetName()); err != nil {
				failed++
				lastErr = err.Error()
				continue
			}
			killed++
		}
		a.clearMarks()
		if failed > 0 {
			a.Flash(FlashError, "killed %d/%d %s — last error: %s", killed, len(objs), gvr.Resource, lastErr)
			return
		}
		a.Flash(FlashInfo, "killed %d %s (grace 0)", killed, gvr.Resource)
	}
	summary := fmt.Sprintf("Force-kill %d %s (grace 0):\n%s", len(objs), gvr.Resource, strings.Join(names, "\n"))
	a.confirmDestructive(summary, safety.Delete, run)
}

// confirmDestructive runs fn after a guard-aware confirmation for verb.
func (a *App) confirmDestructive(summary string, verb safety.Verb, fn func()) {
	ctxName := a.clientContext()
	if safety.RequiresConfirmation(a.options.Config, ctxName, verb) {
		input := promptField(fmt.Sprintf("Type %s to confirm: ", ctxName), func(text string) {
			if err := safety.Authorize(a.options.Config, ctxName, verb, text); err != nil {
				a.showMessage("Blocked", err.Error())
				return
			}
			a.closeModal()
			fn()
		}, a.closeModal)
		a.showModal(input)
		return
	}
	a.confirmModal(summary, fn)
}

// showAttach attaches to the selected pod's container stdio.
func (a *App) showAttach() {
	pod, ok := a.selectedPod()
	if !ok {
		a.Flash(FlashInfo, "attach is only available for pods")
		return
	}
	container := ""
	if len(pod.Spec.Containers) > 0 {
		container = pod.Spec.Containers[0].Name
	}
	target := actions.ExecTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container}
	if a.options.Client == nil || a.options.Client.Config == nil || a.options.Client.Clientset == nil {
		a.showMessage("Attach", fmt.Sprintf("kubectl attach -n %s %s -c %s", target.Namespace, target.Pod, target.Container))
		return
	}
	var runErr error
	run := func() {
		runErr = actions.RunAttach(context.Background(), a.options.Client.Config, a.options.Client.Clientset, target, os.Stdin, os.Stdout, os.Stderr)
	}
	if !a.app.Suspend(run) {
		run()
	}
	if runErr != nil {
		a.showMessage("Attach failed", runErr.Error())
		return
	}
	a.showMessage("Attach", "session ended")
}

// showCordon toggles a node's schedulable state (c cordon / u uncordon are
// bound separately but share this with the desired value).
func (a *App) setCordon(unschedulable bool) {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Node" {
		a.Flash(FlashInfo, "cordon is only available for nodes")
		return
	}
	name := obj.GetName()
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		a.Flash(FlashError, "kubernetes clientset is required")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := actions.SetNodeSchedulable(ctx, a.options.Client.Clientset, name, unschedulable); err != nil {
		a.Flash(FlashError, "cordon failed: %s", err.Error())
		return
	}
	if unschedulable {
		a.Flash(FlashInfo, "cordoned %s", name)
	} else {
		a.Flash(FlashInfo, "uncordoned %s", name)
	}
}

// showDrain drains the selected node with a progress view. Drain always
// requires a typed confirmation.
func (a *App) showDrain() {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Node" {
		a.Flash(FlashInfo, "drain is only available for nodes")
		return
	}
	name := obj.GetName()
	ctxName := a.clientContext()
	if err := safety.Authorize(a.options.Config, ctxName, safety.Delete, ""); err == safety.ErrReadOnly {
		a.showMessage("Blocked", "context is read-only")
		return
	}
	input := promptField(fmt.Sprintf("Type %s to DRAIN node %s: ", ctxName, name), func(text string) {
		if text != ctxName {
			a.showMessage("Blocked", fmt.Sprintf("type the context name %q to confirm drain", ctxName))
			return
		}
		a.closeModal()
		a.runDrain(name)
	}, a.closeModal)
	a.showModal(input)
}

func (a *App) runDrain(name string) {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		a.Flash(FlashError, "kubernetes clientset is required to drain")
		return
	}
	output := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetText("Draining " + name + "...\n")
	output.SetTitle(" drain " + name + " ").SetBorder(true)
	output.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	a.pushView(output, output, "drain:"+name, cancel)
	opts := actions.DrainOptions{Timeout: 2 * time.Minute, IgnoreDaemonSets: true}
	emit := func(line string) {
		a.app.QueueUpdateDraw(func() { fmt.Fprintln(output, line) })
	}
	go func() {
		res, err := actions.DrainNode(ctx, a.options.Client.Clientset, name, opts, emit)
		if err != nil {
			emit("error: " + err.Error())
			a.Flash(FlashError, "drain %s: %s", name, err.Error())
			return
		}
		a.Flash(FlashInfo, "drained %s: %d evicted, %d skipped", name, res.Evicted, res.Skipped)
	}()
}

// showRollout opens a revision picker for the selected workload; Enter rolls
// back to the chosen revision.
func (a *App) showRollout() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	kind := obj.GetKind()
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
	default:
		a.Flash(FlashInfo, "rollout is only available for deployments/statefulsets/daemonsets")
		return
	}
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		a.Flash(FlashError, "kubernetes clientset is required")
		return
	}
	ns, name := obj.GetNamespace(), obj.GetName()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	revs, err := actions.RolloutRevisions(ctx, a.options.Client.Clientset, kind, ns, name)
	if err != nil {
		a.Flash(FlashError, "rollout history: %s", err.Error())
		return
	}
	if len(revs) < 2 {
		a.Flash(FlashInfo, "no previous revision to roll back to")
		return
	}
	list := tview.NewList().ShowSecondaryText(true)
	for _, r := range revs {
		rev := r
		cause := rev.Cause
		if cause == "" {
			cause = "(no change-cause)"
		}
		list.AddItem(fmt.Sprintf("revision %d", rev.Number), cause, 0, func() {
			a.closeModal()
			a.runRollout(kind, ns, name, rev.Number)
		})
	}
	list.SetTitle(fmt.Sprintf(" rollback %s/%s ", kind, name)).SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.closeModal()
			return nil
		}
		return event
	})
	a.showModal(list)
}

func (a *App) runRollout(kind, ns, name string, toRevision int64) {
	ctxName := a.clientContext()
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := actions.RolloutUndo(ctx, a.options.Client.Clientset, kind, ns, name, toRevision); err != nil {
			a.Flash(FlashError, "rollout undo: %s", err.Error())
			return
		}
		a.Flash(FlashInfo, "rolled %s/%s back to revision %d", kind, name, toRevision)
	}
	if err := safety.Authorize(a.options.Config, ctxName, safety.Restart, ""); err == safety.ErrReadOnly {
		a.showMessage("Blocked", "context is read-only")
		return
	}
	if safety.RequiresConfirmation(a.options.Config, ctxName, safety.Restart) {
		a.confirmDestructive(fmt.Sprintf("roll %s/%s back to revision %d", kind, name, toRevision), safety.Restart, run)
		return
	}
	run()
}

// showSecretDecode renders a Secret's data with base64 values decoded.
func (a *App) showSecretDecode() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	secret, ok := toTyped[corev1.Secret](obj)
	if !ok {
		a.Flash(FlashError, "could not decode secret %s", obj.GetName())
		return
	}
	a.showDetailContent(views.DetailContent{Title: "secret decode: " + secret.Name, Body: decodeSecretBody(secret)})
}

// decodeSecretBody renders a secret's data with values decoded, binary-safe.
func decodeSecretBody(secret corev1.Secret) string {
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "Secret %s/%s (type %s)\n\n", secret.Namespace, secret.Name, secret.Type)
	if len(keys) == 0 {
		b.WriteString("<no data>")
	}
	for _, k := range keys {
		value := secret.Data[k]
		if utf8.Valid(value) {
			fmt.Fprintf(&b, "%s:\n%s\n\n", k, string(value))
		} else {
			fmt.Fprintf(&b, "%s:\n<binary: %d bytes, base64: %s>\n\n", k, len(value), base64.StdEncoding.EncodeToString(value))
		}
	}
	return b.String()
}
