package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/alphabravo-oss/astrotui/internal/safety"
)

func (a *App) showCopyPlan() {
	currentContext := a.currentContext()
	if safety.RequiresConfirmation(a.options.Config, currentContext, safety.Copy) {
		input := tview.NewInputField().SetLabel(fmt.Sprintf("Type %s to copy Pod files: ", currentContext)).SetFieldWidth(40)
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				a.confirmCopyPlan(input.GetText(), "")
				return
			}
			a.app.SetRoot(a.table, true)
		})
		a.app.SetRoot(input, true)
		return
	}
	a.confirmCopyPlan("", "")
}

func (a *App) confirmCopyPlan(confirmText, container string) {
	if err := safety.Authorize(a.options.Config, a.currentContext(), safety.Copy, confirmText); err != nil {
		a.showMessage("Copy unavailable", err.Error())
		return
	}
	pod, ok := a.selectedPod()
	if !ok {
		a.showMessage("Copy unavailable", "copy is implemented for Pod selections")
		return
	}
	containers := logs.ContainerNames(pod)
	if container == "" && len(containers) > 1 {
		a.showCopyContainerPicker(confirmText, pod)
		return
	}
	if container == "" {
		container, ok = logs.DefaultContainer(pod)
		if !ok {
			a.showMessage("Copy unavailable", "Pod has no containers")
			return
		}
	}
	a.showCopyDirection(confirmText, pod, container)
}

func (a *App) showCopyContainerPicker(confirmText string, pod corev1.Pod) {
	list := tview.NewList().ShowSecondaryText(false)
	for _, name := range logs.ContainerNames(pod) {
		container := name
		list.AddItem(container, "", 0, func() { a.confirmCopyPlan(confirmText, container) })
	}
	list.SetTitle(" Select copy container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) showCopyDirection(confirmText string, pod corev1.Pod, container string) {
	list := tview.NewList().ShowSecondaryText(true)
	list.AddItem("From Pod", "Download one remote file or directory into a local directory", 0, func() {
		a.showCopyForm(confirmText, pod, container, actions.CopyFromPod)
	})
	list.AddItem("To Pod", "Upload one local file or directory into a remote directory", 0, func() {
		a.showCopyForm(confirmText, pod, container, actions.CopyToPod)
	})
	list.SetTitle(" Copy direction ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func (a *App) showCopyForm(confirmText string, pod corev1.Pod, container string, direction actions.CopyDirection) {
	localDefault, remoteDefault := ".", "/tmp"
	local := tview.NewInputField().SetLabel("Local path: ").SetText(localDefault).SetFieldWidth(60)
	remote := tview.NewInputField().SetLabel("Remote path: ").SetText(remoteDefault).SetFieldWidth(60)
	form := tview.NewForm().AddFormItem(local).AddFormItem(remote)
	form.AddButton("Copy", func() {
		spec := actions.CopySpec{
			Direction: direction, Namespace: pod.Namespace, Pod: pod.Name, Container: container,
			LocalPath: strings.TrimSpace(local.GetText()), RemotePath: strings.TrimSpace(remote.GetText()),
		}
		a.runCopy(confirmText, spec)
	})
	form.AddButton("Cancel", func() { a.app.SetRoot(a.table, true) })
	form.SetTitle(" Copy " + string(direction) + " ").SetBorder(true)
	a.app.SetRoot(form, true)
}

func (a *App) runCopy(confirmText string, spec actions.CopySpec) {
	if err := safety.Authorize(a.options.Config, a.currentContext(), safety.Copy, confirmText); err != nil {
		a.showMessage("Copy unavailable", err.Error())
		return
	}
	if err := actions.ValidateCopySpec(spec); err != nil {
		a.showMessage("Copy unavailable", err.Error())
		return
	}
	if err := a.authorizePodSubresource(spec.Namespace, spec.Pod, "exec"); err != nil {
		a.showMessage("Copy unavailable", err.Error())
		return
	}
	runner := a.options.CopyRunner
	if runner == nil {
		if a.options.Client == nil || a.options.Client.Config == nil || a.options.Client.Clientset == nil {
			direction := fmt.Sprintf("%s/%s:%s -> %s", spec.Namespace, spec.Pod, spec.RemotePath, spec.LocalPath)
			if spec.Direction == actions.CopyToPod {
				direction = fmt.Sprintf("%s -> %s/%s:%s", spec.LocalPath, spec.Namespace, spec.Pod, spec.RemotePath)
			}
			a.showMessage("Copy plan", direction+"\ncontainer="+spec.Container)
			return
		}
		runner = func(ctx context.Context, spec actions.CopySpec) error {
			exec := func(ctx context.Context, target actions.ExecTarget, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
				return actions.RunExecStream(ctx, a.options.Client.Config, a.options.Client.Clientset, target, stdin, stdout, stderr, tty)
			}
			if spec.Direction == actions.CopyFromPod {
				return actions.CopyFromContainer(ctx, exec, spec)
			}
			return actions.CopyToContainer(ctx, exec, spec)
		}
	}
	var runErr error
	run := func() { runErr = runner(a.context(), spec) }
	if !a.app.Suspend(run) {
		run()
	}
	if runErr != nil {
		a.showMessage("Copy failed", runErr.Error())
		return
	}
	location := spec.LocalPath
	if spec.Direction == actions.CopyToPod {
		location = spec.RemotePath
	}
	if absolute, err := os.Getwd(); err == nil && location == "." {
		location = absolute
	}
	a.showMessage("Copy complete", location)
}
