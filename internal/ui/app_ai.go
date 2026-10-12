//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/yaml"
	"sort"
	"strings"
	"time"
)

func (a *App) showFailureAI() {
	if a.options.AIProvider == nil {
		a.showMessage("AI disabled", "Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	pod, ok := a.selectedPod()
	if !ok {
		a.showMessage("AI explain failure unavailable", "select a failing pod")
		return
	}
	if reason := podFailureReason(pod); reason == "" {
		a.showMessage("AI explain failure unavailable", fmt.Sprintf("pod/%s is not currently failing", pod.Name))
		return
	}
	client := a.options.Client
	a.operation("Prepare failure analysis", func(ctx context.Context) (func(), error) {
		// A private helper captures the client so context changes cannot retarget it.
		worker := &App{options: Options{Client: client}}
		spec, status, events, err := worker.podFailureContext(ctx, pod)
		if err != nil {
			return nil, err
		}
		return func() {
			view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
			view.SetTitle(" AI failure analysis · Esc back ").SetBorder(true)
			view.SetInputCapture(a.backCapture())
			a.streamAIResponse(view, ai.FailureRequest(spec, status, events))
		}, nil
	})
}

func (a *App) podFailureContext(ctx context.Context, pod corev1.Pod) (string, string, string, error) {
	spec, err := yaml.Marshal(pod.Spec)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal pod spec: %w", err)
	}
	status, err := yaml.Marshal(pod.Status)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal pod status: %w", err)
	}
	return string(spec), string(status), a.podEventSummary(ctx, pod), nil
}

func (a *App) podEventSummary(ctx context.Context, pod corev1.Pod) string {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return "No recent events available."
	}
	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.uid", string(pod.UID)),
		fields.OneTermEqualSelector("involvedObject.kind", "Pod"),
	)
	list, err := a.options.Client.Clientset.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil || len(list.Items) == 0 {
		return "No recent events available."
	}
	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].LastTimestamp.Before(&list.Items[j].LastTimestamp)
	})
	lines := make([]string, 0, len(list.Items))
	for _, event := range list.Items {
		lines = append(lines, fmt.Sprintf("%s %s %s", event.Type, event.Reason, event.Message))
	}
	return strings.Join(lines, "\n")
}

func podFailureReason(pod corev1.Pod) string {
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			return status.State.Waiting.Reason
		}
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			if status.State.Terminated.Reason != "" {
				return status.State.Terminated.Reason
			}
			return fmt.Sprintf("exit code %d", status.State.Terminated.ExitCode)
		}
	}
	switch pod.Status.Phase {
	case corev1.PodFailed, corev1.PodPending:
		return string(pod.Status.Phase)
	default:
		return ""
	}
}

func (a *App) showManifestAI(view *tview.TextView, manifest string) {
	if a.options.AIProvider == nil {
		view.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	a.streamAIResponse(view, ai.ManifestRequest(manifest))
}

func (a *App) streamAIResponse(view *tview.TextView, req ai.Request) {
	a.previewAI(req, func(prepared ai.Request) { a.app.SetRoot(view, true); a.sendAIResponse(view, prepared) }, func() { a.app.SetRoot(view, true) })
}

func (a *App) sendAIResponse(view *tview.TextView, req ai.Request) {
	view.SetText("Asking AI...")
	if a.aiCancel != nil {
		a.aiCancel()
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), 30*time.Second)
	a.aiCancel = cancel
	oldCapture := view.GetInputCapture()
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc || e.Key() == tcell.KeyRune && e.Rune() == 'q' {
			cancel()
		}
		if oldCapture != nil {
			return oldCapture(e)
		}
		return e
	})
	provider := a.options.AIProvider
	go func() {
		defer cancel()
		chunks, err := provider.Stream(ctx, req)
		if err != nil {
			a.queueUpdate(a.backgroundContext(), func() { view.SetText("AI failed: " + err.Error()) })
			return
		}
		var b strings.Builder
		for chunk := range chunks {
			if chunk.Err != nil {
				errText := chunk.Err.Error()
				a.queueUpdate(a.backgroundContext(), func() { view.SetText("AI failed: " + errText) })
				return
			}
			if chunk.Text != "" {
				if b.Len()+len(chunk.Text) > 1024*1024 {
					a.queueUpdate(a.backgroundContext(), func() { view.SetText("AI response exceeded 1 MiB") })
					return
				}
				b.WriteString(chunk.Text)
				text := b.String()
				a.queueUpdate(a.backgroundContext(), func() { view.SetText(tview.Escape(text)) })
			}
			if chunk.Done {
				return
			}
		}
	}()
}
