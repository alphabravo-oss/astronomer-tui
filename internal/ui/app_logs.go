//revive:disable:exported package-comments
package ui

import (
	"bufio"
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"strconv"
	"strings"
	"time"
)

type logViewOptions struct {
	TailLines    *int64
	SinceSeconds *int64
	Follow       bool
	Previous     bool
	Timestamps   bool
}

func defaultLogViewOptions() logViewOptions {
	tail := int64(500)
	return logViewOptions{TailLines: &tail, Follow: true}
}

func (a *App) showLogsPlan() {
	if pod, ok := a.selectedPod(); ok && len(logs.ContainerNames(pod)) > 1 {
		a.showLogContainerPicker(pod)
		return
	}
	targets, err := a.selectedLogTargets(context.Background())
	if err != nil {
		a.showMessage("Logs unavailable", err.Error())
		return
	}
	a.showLogsViewWithOptions(targets, defaultLogViewOptions())
}

func (a *App) showLogsView(targets []logs.PodLogTarget) {
	a.showLogsViewWithOptions(targets, defaultLogViewOptions())
}

func (a *App) showLogOptionsPlan() {
	targets, err := a.selectedLogTargets(context.Background())
	if err != nil {
		a.showMessage("Logs unavailable", err.Error())
		return
	}
	tail, since := "500", ""
	previous, timestamps, follow := false, false, true
	form := tview.NewForm().
		AddInputField("Tail lines", tail, 12, nil, func(value string) { tail = value }).
		AddInputField("Since seconds", since, 12, nil, func(value string) { since = value }).
		AddCheckbox("Previous container", previous, func(value bool) { previous = value }).
		AddCheckbox("Server timestamps", timestamps, func(value bool) { timestamps = value }).
		AddCheckbox("Follow", follow, func(value bool) { follow = value })
	form.AddButton("Open", func() {
		opts := logViewOptions{Previous: previous, Timestamps: timestamps, Follow: follow && !previous}
		if strings.TrimSpace(tail) != "" {
			value, parseErr := strconv.ParseInt(strings.TrimSpace(tail), 10, 64)
			if parseErr != nil || value < 0 {
				a.showMessage("Invalid log options", "tail lines must be a non-negative integer")
				return
			}
			opts.TailLines = &value
		}
		if strings.TrimSpace(since) != "" {
			value, parseErr := strconv.ParseInt(strings.TrimSpace(since), 10, 64)
			if parseErr != nil || value < 0 {
				a.showMessage("Invalid log options", "since seconds must be a non-negative integer")
				return
			}
			opts.SinceSeconds = &value
		}
		a.showLogsViewWithOptions(targets, opts)
	})
	form.AddButton("Cancel", func() { a.app.SetRoot(a.table, true) })
	form.SetTitle(" Log options ").SetBorder(true)
	a.app.SetRoot(form, true)
}

func (a *App) showLogsViewWithOptions(targets []logs.PodLogTarget, opts logViewOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	buffer := logs.NewBuffer(10_000)
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	wrap := true
	view.SetWrap(wrap).SetWordWrap(true)
	view.SetTitle(logViewTitle(opts)).SetBorder(true)
	filter := ""
	search := logSearchState{}
	gotoPending := false
	render := func() {
		view.SetText(renderLogEntries(buffer.Filter(filter), true))
	}
	render()
	streamer := a.logStreamer()
	for _, target := range targets {
		target := target
		go func() {
			source := target.Pod
			if target.Container != "" {
				source += "/" + target.Container
			}
			writer := newLogLineWriter(source, buffer, func() {
				a.app.QueueUpdateDraw(render)
			})
			request := logs.RequestOptions{TailLines: opts.TailLines, SinceSeconds: opts.SinceSeconds, Follow: opts.Follow, Previous: opts.Previous, Timestamps: opts.Timestamps}
			if err := streamer(ctx, target, request, writer); err != nil {
				buffer.Append(source, "stream error: "+err.Error(), time.Now())
				a.app.QueueUpdateDraw(render)
			}
		}()
	}
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			cancel()
			a.app.SetRoot(a.table, true)
			return nil
		case event.Rune() == '/':
			a.showLogFilter(view, buffer, &filter)
			return nil
		case event.Key() == tcell.KeyCtrlR:
			a.showLogRegex(view, buffer, &search)
			return nil
		case event.Rune() == 'n':
			if row, ok := search.Next(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Rune() == 'N':
			if row, ok := search.Prev(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Rune() == 's':
			a.showLogSave(view, buffer, filter)
			return nil
		case event.Rune() == 'g':
			gotoPending = true
			view.SetTitle(" Logs: g e events | g o owner | g a AI ")
			return nil
		case gotoPending && event.Rune() == 'e':
			gotoPending = false
			a.jumpLogEvents(view, buffer, filter)
			return nil
		case gotoPending && event.Rune() == 'o':
			gotoPending = false
			a.jumpLogOwner(context.Background(), view, buffer, filter)
			return nil
		case gotoPending && event.Rune() == 'a':
			gotoPending = false
			a.showLogAISummary(view, buffer, filter)
			return nil
		case event.Rune() == 'w':
			toggleLogWrap(view, &wrap)
			return nil
		case event.Key() == tcell.KeyCtrlE:
			a.showLogAIChat(view, buffer, filter)
			return nil
		case event.Rune() == 'a':
			a.showLogAISummary(view, buffer, filter)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func logViewTitle(opts logViewOptions) string {
	parts := []string{" Logs"}
	if opts.Previous {
		parts = append(parts, "previous")
	}
	if opts.TailLines != nil {
		parts = append(parts, fmt.Sprintf("tail=%d", *opts.TailLines))
	}
	if opts.SinceSeconds != nil {
		parts = append(parts, fmt.Sprintf("since=%ds", *opts.SinceSeconds))
	}
	if opts.Follow {
		parts = append(parts, "follow")
	}
	return strings.Join(parts, " ") + " "
}

func (a *App) jumpLogEvents(view *tview.TextView, buffer *logs.Buffer, filter string) {
	entry, ok := selectedLogEntry(view, buffer, filter)
	if !ok || strings.TrimSpace(entry.Pod) == "" {
		view.SetText("No pod log line selected.")
		return
	}
	a.switchResourceFiltered(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "events"}, logEntryPod(entry.Pod))
	a.app.SetRoot(a.table, true)
}

func (a *App) jumpLogOwner(ctx context.Context, view *tview.TextView, buffer *logs.Buffer, filter string) {
	entry, ok := selectedLogEntry(view, buffer, filter)
	if !ok || strings.TrimSpace(entry.Pod) == "" {
		view.SetText("No pod log line selected.")
		return
	}
	pod, err := a.podForLogEntry(ctx, entry)
	if err != nil {
		view.SetText("Owner jump unavailable: " + err.Error())
		return
	}
	if len(pod.OwnerReferences) == 0 {
		view.SetText("Owner jump unavailable: pod has no owner references")
		return
	}
	owner := pod.OwnerReferences[0]
	gvr, ok := ownerGVR(owner.Kind)
	if !ok {
		view.SetText("Owner jump unavailable: unsupported owner kind " + owner.Kind)
		return
	}
	a.switchResourceFiltered(ctx, gvr, owner.Name)
	a.app.SetRoot(a.table, true)
}

func (a *App) switchResourceFiltered(ctx context.Context, gvr schema.GroupVersionResource, filter string) bool {
	if !a.switchResource(ctx, gvr) {
		return false
	}
	a.mu.Lock()
	a.filter = filter
	a.mu.Unlock()
	a.renderCurrent()
	a.updateTitle()
	return true
}

func selectedLogEntry(view *tview.TextView, buffer *logs.Buffer, filter string) (logs.Entry, bool) {
	entries := buffer.Filter(filter)
	if len(entries) == 0 {
		return logs.Entry{}, false
	}
	row, _ := view.GetScrollOffset()
	if row < 0 {
		row = 0
	}
	if row >= len(entries) {
		row = len(entries) - 1
	}
	return entries[row], true
}

func (a *App) podForLogEntry(ctx context.Context, entry logs.Entry) (corev1.Pod, error) {
	podName := logEntryPod(entry.Pod)
	if pod, ok := a.selectedPod(); ok && pod.Name == podName {
		return pod, nil
	}
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return corev1.Pod{}, fmt.Errorf("kubernetes clientset is required")
	}
	namespace := a.options.Client.Namespace
	if podName != "" {
		targets, err := a.selectedLogTargets(ctx)
		if err == nil {
			for _, target := range targets {
				if target.Pod == podName && target.Namespace != "" {
					namespace = target.Namespace
					break
				}
			}
		}
	}
	pod, err := a.options.Client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return corev1.Pod{}, err
	}
	return *pod, nil
}

func logEntryPod(source string) string {
	pod, _, _ := strings.Cut(source, "/")
	return pod
}

func ownerGVR(kind string) (schema.GroupVersionResource, bool) {
	switch kind {
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true
	case "StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true
	case "DaemonSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true
	case "ReplicaSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, true
	case "Job":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, true
	case "CronJob":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, true
	default:
		return schema.GroupVersionResource{}, false
	}
}

func (a *App) showLogContainerPicker(pod corev1.Pod) {
	list := tview.NewList()
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.showLogsView([]logs.PodLogTarget{logTargetForPodContainer(pod, container)})
		})
	}
	list.SetTitle(" Select log container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func logTargetForPodContainer(pod corev1.Pod, container string) logs.PodLogTarget {
	return logs.PodLogTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container}
}

func toggleLogWrap(view *tview.TextView, wrap *bool) {
	*wrap = !*wrap
	view.SetWrap(*wrap)
}

func (a *App) showLogFilter(view *tview.TextView, buffer *logs.Buffer, filter *string) {
	input := tview.NewInputField().
		SetLabel("Log filter: ").
		SetText(*filter).
		SetFieldWidth(50)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			*filter = input.GetText()
			view.SetText(renderLogEntries(buffer.Filter(*filter), true))
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogRegex(view *tview.TextView, buffer *logs.Buffer, search *logSearchState) {
	input := tview.NewInputField().
		SetLabel("Regex: ").
		SetFieldWidth(50)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			matches, err := buffer.RegexMatches(input.GetText())
			if err != nil {
				view.SetText("regex error: " + err.Error())
			} else {
				*search = newLogSearchState(matches)
				view.SetText(fmt.Sprintf("%d matches\n\n%s", len(matches), renderLogEntries(buffer.Entries(), true)))
			}
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogSave(view *tview.TextView, buffer *logs.Buffer, filter string) {
	input := tview.NewInputField().
		SetLabel("Save logs: ").
		SetFieldWidth(60)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			path := strings.TrimSpace(input.GetText())
			if path != "" {
				if err := buffer.Save(path, filter); err != nil {
					view.SetText("save error: " + err.Error())
				} else {
					view.SetText(fmt.Sprintf("saved logs to %s\n\n%s", path, renderLogEntries(buffer.Filter(filter), true)))
				}
			}
		}
		a.app.SetRoot(view, true)
	})
	a.app.SetRoot(input, true)
}

func (a *App) showLogAISummary(view *tview.TextView, buffer *logs.Buffer, filter string) {
	if a.options.AIProvider == nil {
		view.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	prompt := logSummaryContext(buffer.Filter(filter), 100)
	if strings.TrimSpace(prompt) == "" {
		view.SetText("No log lines available to summarize")
		return
	}
	a.streamAIResponse(view, ai.LogSummaryRequest(prompt))
}

func (a *App) showLogAIChat(logView *tview.TextView, buffer *logs.Buffer, filter string) {
	if a.options.AIProvider == nil {
		logView.SetText("AI is disabled. Configure ai.enabled and ai.provider in ~/.config/astrotui/config.yaml")
		return
	}
	contextText := logSummaryContext(buffer.Filter(filter), 100)
	if strings.TrimSpace(contextText) == "" {
		logView.SetText("No log lines available for AI.")
		return
	}
	output := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText("Ask a follow-up about these logs.")
	output.SetTitle(" AI logs ").SetBorder(true)
	input := tview.NewInputField().SetLabel("Ask: ").SetFieldWidth(0)
	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			question := strings.TrimSpace(input.GetText())
			if question == "" {
				return
			}
			input.SetText("")
			a.streamAIResponse(output, ai.LogQuestionRequest(contextText, question))
		case tcell.KeyEsc:
			a.app.SetRoot(logView, true)
		}
	})
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(output, 0, 1, false).
		AddItem(input, 1, 0, true)
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.app.SetRoot(logView, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(flex, true)
}

func (a *App) logStreamer() func(context.Context, logs.PodLogTarget, logs.RequestOptions, io.Writer) error {
	if a.options.LogStreamer != nil {
		return a.options.LogStreamer
	}
	a.mu.RLock()
	client := a.options.Client
	a.mu.RUnlock()
	return func(ctx context.Context, target logs.PodLogTarget, opts logs.RequestOptions, writer io.Writer) error {
		if client == nil || client.Clientset == nil {
			return fmt.Errorf("kubernetes clientset is required for log streaming")
		}
		return logs.StreamPodLogs(ctx, client.Clientset, target, opts, writer)
	}
}

func renderLogEntries(entries []logs.Entry, prefixPod bool) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, formatLogEntryForView(entry, prefixPod))
	}
	return strings.Join(lines, "\n")
}

func formatLogEntryForView(entry logs.Entry, prefixPod bool) string {
	if !prefixPod || entry.Pod == "" {
		return tview.Escape(logs.FormatEntry(entry, prefixPod))
	}
	colored := fmt.Sprintf("[%s]%s[-]", logColor(entry.Color), tview.Escape(logs.PrefixPod(entry.Pod, entry.Line)))
	if !entry.Time.IsZero() {
		return fmt.Sprintf("%s %s", entry.Time.Format(time.RFC3339), colored)
	}
	return colored
}

func logColor(index int) string {
	colors := []string{"red", "green", "yellow", "blue", "purple", "aqua", "orange", "teal", "maroon", "lime", "fuchsia", "navy", "olive", "silver", "white", "gray"}
	if index < 0 {
		index = -index
	}
	return colors[index%len(colors)]
}

func logSummaryContext(entries []logs.Entry, limit int) string {
	if limit <= 0 || len(entries) <= limit {
		return renderLogEntries(entries, true)
	}
	return renderLogEntries(entries[len(entries)-limit:], true)
}

type logLineWriter struct {
	pod        string
	buffer     *logs.Buffer
	onAppend   func()
	pending    string
	lastNotify time.Time
}

type logSearchState struct {
	matches []int
	current int
}

func newLogSearchState(matches []int) logSearchState {
	return logSearchState{matches: append([]int(nil), matches...), current: -1}
}

func (s *logSearchState) Next() (int, bool) {
	if len(s.matches) == 0 {
		return 0, false
	}
	s.current = (s.current + 1) % len(s.matches)
	return s.matches[s.current], true
}

func (s *logSearchState) Prev() (int, bool) {
	if len(s.matches) == 0 {
		return 0, false
	}
	if s.current < 0 {
		s.current = 0
	}
	s.current = (s.current - 1 + len(s.matches)) % len(s.matches)
	return s.matches[s.current], true
}

func newLogLineWriter(pod string, buffer *logs.Buffer, onAppend func()) *logLineWriter {
	return &logLineWriter{pod: pod, buffer: buffer, onAppend: onAppend}
}

func (w *logLineWriter) Write(data []byte) (int, error) {
	w.pending += string(data)
	if len(w.pending) > logs.MaxLineBytes {
		w.buffer.Append(w.pod, w.pending[:logs.MaxLineBytes]+"…[truncated]", time.Now())
		w.pending = ""
		w.notify()
	}
	scanner := bufio.NewScanner(strings.NewReader(w.pending))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if strings.HasSuffix(w.pending, "\n") {
		w.pending = ""
	} else if len(lines) > 0 {
		w.pending = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		w.buffer.Append(w.pod, line, time.Now())
	}
	if len(lines) > 0 {
		w.notify()
	}
	return len(data), scanner.Err()
}

func (w *logLineWriter) notify() {
	if w.onAppend == nil || time.Since(w.lastNotify) < 50*time.Millisecond {
		return
	}
	w.lastNotify = time.Now()
	w.onAppend()
}

func (a *App) selectedLogTargets(ctx context.Context) ([]logs.PodLogTarget, error) {
	obj := a.selectedObject()
	if obj == nil {
		return nil, fmt.Errorf("no resource selected")
	}
	switch obj.GetKind() {
	case "Pod":
		pod, ok := toTyped[corev1.Pod](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode pod %s", obj.GetName())
		}
		return logs.TargetsForPods([]corev1.Pod{pod}), nil
	case "Deployment":
		deployment, ok := toTyped[appsv1.Deployment](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode deployment %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, deployment.Namespace, deployment.Spec.Selector)
	case "StatefulSet":
		statefulSet, ok := toTyped[appsv1.StatefulSet](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode statefulset %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, statefulSet.Namespace, statefulSet.Spec.Selector)
	case "DaemonSet":
		daemonSet, ok := toTyped[appsv1.DaemonSet](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode daemonset %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, daemonSet.Namespace, daemonSet.Spec.Selector)
	case "ReplicaSet":
		replicaSet, ok := toTyped[appsv1.ReplicaSet](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode replicaset %s", obj.GetName())
		}
		return a.logTargetsForSelector(ctx, replicaSet.Namespace, replicaSet.Spec.Selector)
	case "Job":
		job, ok := toTyped[batchv1.Job](obj)
		if !ok {
			return nil, fmt.Errorf("could not decode job %s", obj.GetName())
		}
		selector := job.Spec.Selector
		if selector == nil {
			selector = &metav1.LabelSelector{MatchLabels: map[string]string{batchv1.JobNameLabel: job.Name}}
		}
		return a.logTargetsForSelector(ctx, job.Namespace, selector)
	default:
		return nil, fmt.Errorf("logs are implemented for pod, workload, and job selections")
	}
}

func (a *App) logTargetsForSelector(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]logs.PodLogTarget, error) {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return nil, fmt.Errorf("kubernetes clientset is required for workload logs")
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}
	if parsed.Empty() {
		return nil, fmt.Errorf("workload has an empty pod selector")
	}
	pods, err := logs.PodsForSelector(ctx, a.options.Client.Clientset, namespace, parsed)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods match selector %s", parsed.String())
	}
	return logs.TargetsForPodsAllContainers(pods), nil
}
