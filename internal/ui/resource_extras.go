package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/gdamore/tcell/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/yaml"
)

func (a *App) showNodeShell() {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Node" {
		a.showMessage("Node shell", "Select a Node first")
		return
	}
	obj = obj.DeepCopy()
	client := a.options.Client
	if client == nil || client.Clientset == nil || client.Config == nil {
		a.showMessage("Node shell", "Kubernetes connection unavailable")
		return
	}
	ns := client.Namespace
	a.confirmExternalMutation("Node shell", fmt.Sprintf("Open a privileged root shell on node %s in context %s?\nA temporary host-PID/network helper pod will be created and deleted on exit. Ctrl-C cancels preparation. Maximum helper lifetime: one hour.", obj.GetName(), client.Context), func() {
		var runErr error
		run := func() {
			ctx, stop := signal.NotifyContext(a.backgroundContext(), os.Interrupt)
			defer stop()
			fmt.Fprintln(os.Stderr, "Preparing node shell; Ctrl-C cancels…")
			target, cleanup, err := actions.PrepareNodeShell(ctx, client.Clientset, obj.GetName(), obj.GetUID(), ns, "")
			if err != nil {
				runErr = err
				return
			}
			defer func() { runErr = errors.Join(runErr, cleanup()) }()
			runErr = actions.RunExec(ctx, client.Config, client.Clientset, target, os.Stdin, os.Stdout, os.Stderr)
		}
		if !a.app.Suspend(run) {
			run()
		}
		if runErr != nil {
			a.showMessage("Node shell", runErr.Error())
		} else {
			a.showMessage("Node shell", "Session ended; helper pod deleted")
		}
	})
}
func (a *App) showContainers() {
	pod, ok := a.selectedPod()
	if !ok {
		a.showMessage("Containers", "Select a Pod first")
		return
	}
	type entry struct {
		name, kind, image string
		raw               any
	}
	var entries []entry
	for _, c := range pod.Spec.Containers {
		entries = append(entries, entry{c.Name, "regular", c.Image, c})
	}
	for _, c := range pod.Spec.InitContainers {
		entries = append(entries, entry{c.Name, "init", c.Image, c})
	}
	for _, c := range pod.Spec.EphemeralContainers {
		entries = append(entries, entry{c.Name, "ephemeral", c.Image, c})
	}
	statuses := map[string]corev1.ContainerStatus{}
	for _, list := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses, pod.Status.EphemeralContainerStatuses} {
		for _, s := range list {
			statuses[s.Name] = s
		}
	}
	table := a.explorerTable("Containers · Enter spec/env/status · l logs · x shell · Esc back", []string{"NAME", "KIND", "READY", "RESTARTS", "STATE", "IMAGE"})
	for i, c := range entries {
		s := statuses[c.name]
		state := "unknown"
		if s.State.Running != nil {
			state = "Running"
		}
		if s.State.Waiting != nil {
			state = s.State.Waiting.Reason
		}
		if s.State.Terminated != nil {
			state = s.State.Terminated.Reason
		}
		a.setExplorerRow(table, i+1, c.name, c.kind, fmt.Sprint(s.Ready), fmt.Sprint(s.RestartCount), state, c.image)
	}
	selected := func() (entry, bool) {
		row, _ := table.GetSelection()
		if row < 1 || row > len(entries) {
			return entry{}, false
		}
		return entries[row-1], true
	}
	table.SetSelectedFunc(func(int, int) {
		if c, ok := selected(); ok {
			body, _ := json.MarshalIndent(map[string]any{"spec": c.raw, "status": statuses[c.name]}, "", "  ")
			a.showDocument("Container · "+c.name, string(body))
		}
	})
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		c, ok := selected()
		if !ok {
			return e
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'l' {
			a.showLogsView([]logs.PodLogTarget{logTargetForPodContainer(pod, c.name)})
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'x' {
			current := a.selectedObject()
			if current == nil || current.GetUID() != pod.UID {
				a.showMessage("Container changed", "Reselect the pod before opening a shell")
				return nil
			}
			a.showExecForContainer(c.name)
			return nil
		}
		return e
	})
	a.app.SetRoot(table, true).SetFocus(table)
}
func (a *App) showExecForContainer(container string) {
	contextName := a.options.Client.Context
	a.confirmExternalMutation("Container shell", "Open an interactive shell in context "+contextName+"?", func() { // Confirmation has completed; retain it for the executor policy check.
		a.confirmExecPlanForContainer(contextName, container)
	})
}
func (a *App) showDescribe() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	client := a.options.Client
	a.operation("Describe resource", func(ctx context.Context) (func(), error) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s/%s\nNamespace: %s\nUID: %s\nResourceVersion: %s\n", obj.GetKind(), obj.GetName(), obj.GetNamespace(), obj.GetUID(), obj.GetResourceVersion())
		for _, owner := range obj.GetOwnerReferences() {
			fmt.Fprintf(&b, "Owner: %s/%s (%s)\n", owner.Kind, owner.Name, owner.UID)
		}
		b.WriteString("\nManifest:\n")
		data, err := yaml.Marshal(obj.Object)
		if err != nil {
			return nil, err
		}
		b.Write(data)
		if client != nil && client.Clientset != nil && obj.GetUID() != "" {
			events, err := client.Clientset.CoreV1().Events(obj.GetNamespace()).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", string(obj.GetUID())).String()})
			b.WriteString("\nEvents:\n")
			if err != nil {
				b.WriteString(err.Error())
			} else {
				for _, e := range events.Items {
					fmt.Fprintf(&b, "%s %s: %s\n", e.Type, e.Reason, e.Message)
				}
			}
		}
		return func() { a.showDocument("Describe · "+obj.GetKind()+"/"+obj.GetName(), b.String()) }, nil
	})
}
func objectImages(obj *unstructured.Unstructured) []string {
	if obj == nil {
		return nil
	}
	spec := obj.Object
	switch obj.GetKind() {
	case "CronJob":
		spec, _, _ = unstructured.NestedMap(spec, "spec", "jobTemplate", "spec", "template")
	case "Pod":
	default:
		if template, ok, _ := unstructured.NestedMap(spec, "spec", "template"); ok {
			spec = template
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
		list, _, _ := unstructured.NestedSlice(spec, "spec", field)
		for _, raw := range list {
			if c, ok := raw.(map[string]any); ok {
				if image, ok := c["image"].(string); ok && image != "" && !seen[image] {
					seen[image] = true
					out = append(out, image)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
