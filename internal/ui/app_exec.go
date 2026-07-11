//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"strings"
	"time"
)

func (a *App) showExecPlan() {
	currentContext := ""
	if a.options.Client != nil {
		currentContext = a.options.Client.Context
	}
	if safety.RequiresConfirmation(a.options.Config, currentContext, safety.Exec) {
		input := tview.NewInputField().
			SetLabel(fmt.Sprintf("Type %s to exec into pod: ", currentContext)).
			SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmExecPlan(input.GetText())
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmExecPlan("")
}

func (a *App) showAttachPlan() {
	currentContext := a.currentContext()
	if safety.RequiresConfirmation(a.options.Config, currentContext, safety.Exec) {
		input := tview.NewInputField().SetLabel(fmt.Sprintf("Type %s to attach to pod: ", currentContext)).SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmAttach(input.GetText(), "")
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmAttach("", "")
}

func (a *App) confirmAttach(confirmText, container string) {
	if err := safety.Authorize(a.options.Config, a.currentContext(), safety.Exec, confirmText); err != nil {
		a.showMessage("Attach unavailable", err.Error())
		return
	}
	pod, ok := a.selectedPod()
	if !ok {
		a.showMessage("Attach unavailable", "attach is implemented for pod selections")
		return
	}
	if container == "" && actions.NeedsContainerPicker(pod) {
		a.showAttachContainerPicker(confirmText, pod)
		return
	}
	if container == "" && len(pod.Spec.Containers) > 0 {
		container = pod.Spec.Containers[0].Name
	}
	target := actions.AttachTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container}
	if a.options.AttachRunner == nil && (a.options.Client == nil || a.options.Client.Config == nil || a.options.Client.Clientset == nil) {
		a.showMessage("Attach", fmt.Sprintf("kubectl attach -n %s %s -c %s -it", target.Namespace, target.Pod, target.Container))
		return
	}
	if err := a.authorizePodSubresource(target.Namespace, target.Pod, "attach"); err != nil {
		a.showMessage("Attach unavailable", err.Error())
		return
	}
	runner := a.options.AttachRunner
	if runner == nil {
		runner = func(ctx context.Context, target actions.AttachTarget) error {
			return actions.RunAttach(ctx, a.options.Client.Config, a.options.Client.Clientset, target, os.Stdin, os.Stdout, os.Stderr)
		}
	}
	var runErr error
	run := func() { runErr = runner(context.Background(), target) }
	if !a.app.Suspend(run) {
		run()
	}
	if runErr != nil {
		a.showMessage("Attach failed", runErr.Error())
		return
	}
	a.showMessage("Attach", "session ended")
}

func (a *App) showAttachContainerPicker(confirmText string, pod corev1.Pod) {
	list := tview.NewList().ShowSecondaryText(false)
	for _, item := range pod.Spec.Containers {
		container := item.Name
		list.AddItem(container, "", 0, func() { a.confirmAttach(confirmText, container) })
	}
	list.SetTitle(" Select attach container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) confirmExecPlan(confirmText string) {
	pod, ok := a.selectedPod()
	if ok && actions.NeedsContainerPicker(pod) {
		a.showExecContainerPicker(confirmText, pod)
		return
	}
	a.confirmExecPlanForContainer(confirmText, "")
}

func (a *App) confirmExecPlanForContainer(confirmText, container string) {
	targets, lines, err := a.execTargetsAndPlan(confirmText, container)
	if err != nil {
		a.showMessage("Exec unavailable", err.Error())
		return
	}
	if len(targets) > 0 && a.canRunExec() {
		if err := a.authorizePodSubresource(targets[0].Namespace, targets[0].Pod, "exec"); err != nil {
			a.showMessage("Exec unavailable", err.Error())
			return
		}
		var runErr error
		run := func() {
			runErr = runExecFallback(context.Background(), a.execRunner(), targets)
		}
		if !a.app.Suspend(run) {
			run()
		}
		if runErr != nil {
			a.showMessage("Exec failed", runErr.Error())
			return
		}
		a.showMessage("Exec", "session ended")
		return
	}
	a.showMessage("Exec", strings.Join(lines, "\n"))
}

func runExecFallback(ctx context.Context, runner func(context.Context, actions.ExecTarget) error, targets []actions.ExecTarget) error {
	var lastErr error
	for _, target := range targets {
		if err := runner(ctx, target); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (a *App) execPlanLines(confirmText string) ([]string, error) {
	_, lines, err := a.execTargetsAndPlan(confirmText, "")
	return lines, err
}

func (a *App) execTargetsAndPlan(confirmText, preferredContainer string) ([]actions.ExecTarget, []string, error) {
	currentContext := ""
	if a.options.Client != nil {
		currentContext = a.options.Client.Context
	}
	if err := safety.Authorize(a.options.Config, currentContext, safety.Exec, confirmText); err != nil {
		return nil, nil, err
	}
	pod, ok := a.selectedPod()
	if !ok {
		return nil, nil, fmt.Errorf("exec is currently implemented for pod selections")
	}
	targets, err := actions.ExecTargets(pod, preferredContainer)
	if err != nil {
		return nil, nil, err
	}
	var lines []string
	for _, target := range targets {
		lines = append(lines, fmt.Sprintf("kubectl exec -n %s %s -c %s -- %s", target.Namespace, target.Pod, target.Container, strings.Join(target.Command, " ")))
	}
	return targets, lines, nil
}

func (a *App) showExecContainerPicker(confirmText string, pod corev1.Pod) {
	list := tview.NewList().ShowSecondaryText(false)
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.confirmExecPlanForContainer(confirmText, container)
		})
	}
	list.SetTitle(" Select container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) canRunExec() bool {
	return a.options.ExecRunner != nil || (a.options.Client != nil && a.options.Client.Config != nil && a.options.Client.Clientset != nil)
}

func (a *App) execRunner() func(context.Context, actions.ExecTarget) error {
	if a.options.ExecRunner != nil {
		return a.options.ExecRunner
	}
	return func(ctx context.Context, target actions.ExecTarget) error {
		return actions.RunExec(ctx, a.options.Client.Config, a.options.Client.Clientset, target, os.Stdin, os.Stdout, os.Stderr)
	}
}

func (a *App) authorizePodSubresource(namespace, name, subresource string) error {
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	if client == nil || client.Clientset == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.context(), 2*time.Second)
	defer cancel()
	allowed, reason, err := cluster.CanI(ctx, client, cluster.AccessRequest{
		Verb: "create", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespace: namespace, Name: name, Subresource: subresource,
	})
	if err != nil {
		return fmt.Errorf("RBAC preflight failed: %w", err)
	}
	if !allowed {
		if reason == "" {
			reason = "the active identity is not authorized"
		}
		return fmt.Errorf("RBAC denied pods/%s: %s", subresource, reason)
	}
	return nil
}
