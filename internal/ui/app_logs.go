//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/logs"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"regexp"
	"strings"
	"time"
)

func (a *App) showLogsPlan() {
	if pod, ok := a.selectedPod(); ok && len(logs.ContainerNames(pod)) > 1 {
		a.showLogContainerPicker(pod)
		return
	}
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	client := a.options.Client
	a.operation("Find log targets", func(ctx context.Context) (func(), error) {
		targets, err := logTargetsForObject(ctx, client, obj)
		if err != nil {
			return nil, err
		}
		return func() { a.showLogsView(targets) }, nil
	})
}

func (a *App) showLogsView(targets []logs.PodLogTarget) { a.showLogsOptions(targets, false) }

func (a *App) showLogsOptions(targets []logs.PodLogTarget, previous bool) {
	if a.logCancel != nil {
		a.logCancel()
	}
	ctx, cancel := context.WithCancel(a.backgroundContext())
	a.logCancel = cancel
	a.logRows = nil
	buffer := logs.NewBuffer(10_000)
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	wrap := true
	follow := true
	paused := false
	view.SetWrap(false).SetWordWrap(false).SetRegions(true)
	view.SetTitle(" Logs ").SetBorder(true)
	filter := ""
	search := logSearchState{}
	gotoPending := false
	pattern := ""
	render := func() {
		if paused {
			return
		}
		entries := buffer.Filter(filter)
		text, rows := logDocument(view, entries, wrap)
		a.logRows = rows
		view.SetText(text)
		if pattern != "" {
			re, err := regexp.Compile(pattern)
			if err == nil {
				var matches []int
				last := uint64(0)
				for i, e := range rows {
					if e.ID != last && re.MatchString(e.Line) {
						matches = append(matches, i)
					}
					last = e.ID
				}
				search.matches = matches
			}
		}
		mode := "SCROLL"
		if previous {
			mode = "PREVIOUS"
		}
		if follow {
			view.ScrollToEnd()
			if !previous {
				mode = "FOLLOW"
			}
		}
		view.SetTitle(fmt.Sprintf(" Logs · %s · %d lines · Space pause · F follow · p previous · t retry · / filter · Esc back ", mode, len(entries)))
	}
	render()
	updates := make(chan struct{}, 1)
	notify := func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case <-updates:
					a.queueUpdate(ctx, func() {
						if ctx.Err() == nil {
							render()
						}
					})
				default:
				}
			}
		}
	}()
	streamer := a.logStreamer()
	for _, target := range targets {
		target := target
		go func() {
			writer := newLogLineWriter(target.Pod, buffer, notify)
			writer.target = target
			defer writer.Flush()
			tail := int64(1000)
			if err := streamer(ctx, target, logs.RequestOptions{Follow: !previous, Previous: previous, Timestamps: true, TailLines: &tail}, writer); err != nil && ctx.Err() == nil {
				buffer.AppendSource(target, "stream error (t to retry): "+err.Error(), time.Now())
				notify()
			}
		}()
	}
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyRune && event.Rune() == 'q':
			cancel()
			a.showMain()
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == ' ':
			paused = !paused
			if paused {
				view.SetTitle(" Logs · PAUSED · Space resume · Esc back ")
			} else {
				render()
			}
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'p':
			cancel()
			a.showLogsOptions(targets, !previous)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 't':
			cancel()
			a.showLogsOptions(targets, previous)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'F':
			follow = !follow
			render()
			return nil
		case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyHome || event.Key() == tcell.KeyRune && event.Rune() == 'k':
			follow = false
			return event
		case event.Key() == tcell.KeyRune && event.Rune() == '/':
			a.showLogFilter(view, buffer, &filter, render)
			return nil
		case event.Key() == tcell.KeyCtrlR:
			follow = false
			input := tview.NewInputField().SetLabel("Regex: ").SetText(pattern)
			input.SetDoneFunc(func(key tcell.Key) {
				if key == tcell.KeyEnter {
					if _, err := regexp.Compile(input.GetText()); err != nil {
						input.SetLabel("Invalid regex: ")
						return
					}
					pattern = input.GetText()
					search.current = -1
				}
				a.app.SetRoot(view, true)
				render()
			})
			a.app.SetRoot(input, true)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'n':
			follow = false
			if row, ok := search.Next(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'N':
			follow = false
			if row, ok := search.Prev(); ok {
				view.ScrollTo(row, 0)
			}
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 's':
			a.showLogSave(view, buffer, filter)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'g':
			gotoPending = true
			view.SetTitle(" Logs: g e events | g o owner | g a AI ")
			return nil
		case gotoPending && event.Key() == tcell.KeyRune && event.Rune() == 'e':
			gotoPending = false
			a.jumpLogEvents(view, buffer, filter)
			return nil
		case gotoPending && event.Key() == tcell.KeyRune && event.Rune() == 'o':
			gotoPending = false
			a.jumpLogOwner(context.Background(), view, buffer, filter)
			return nil
		case gotoPending && event.Key() == tcell.KeyRune && event.Rune() == 'a':
			gotoPending = false
			a.showLogAISummary(view, buffer, filter)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'w':
			wrap = !wrap
			render()
			return nil
		case event.Key() == tcell.KeyCtrlE:
			a.showLogAIChat(view, buffer, filter)
			return nil
		case event.Key() == tcell.KeyRune && event.Rune() == 'a':
			a.showLogAISummary(view, buffer, filter)
			return nil
		}
		return event
	})
	a.app.SetRoot(view, true)
}

func (a *App) jumpLogEvents(view *tview.TextView, buffer *logs.Buffer, filter string) {
	a.jumpLogTarget(view, buffer, filter, false)
}
func (a *App) jumpLogOwner(_ context.Context, view *tview.TextView, buffer *logs.Buffer, filter string) {
	a.jumpLogTarget(view, buffer, filter, true)
}
func (a *App) jumpLogTarget(view *tview.TextView, buffer *logs.Buffer, filter string, owner bool) {
	entry, ok := a.currentLogEntry(view, buffer, filter)
	if !ok {
		return
	}
	client := a.options.Client
	ns := entry.Namespace
	if ns == "" {
		ns = client.Namespace
	}
	a.operation("Find log source", func(ctx context.Context) (func(), error) {
		pod, err := client.Clientset.CoreV1().Pods(ns).Get(ctx, entry.Pod, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if entry.PodUID != "" && entry.PodUID != pod.UID {
			return nil, fmt.Errorf("log source pod was replaced; reopen logs for the new pod")
		}
		data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pod)
		if err != nil {
			return nil, err
		}
		obj := &unstructured.Unstructured{Object: data}
		return func() {
			if a.logCancel != nil {
				a.logCancel()
			}
			if owner {
				if len(pod.OwnerReferences) == 0 {
					a.showMessage("Owner", "Pod has no owner")
					return
				}
				a.openOwner(obj, pod.OwnerReferences[0])
			} else {
				a.showObjectEvents(obj)
			}
		}, nil
	})
}

func (a *App) switchResourceFiltered(ctx context.Context, gvr schema.GroupVersionResource, filter string) bool {
	if !a.switchResource(ctx, gvr) {
		return false
	}
	a.mu.Lock()
	a.filter = filter
	a.mu.Unlock()
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
	if pod, ok := a.selectedPod(); ok && pod.Name == entry.Pod {
		return pod, nil
	}
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		return corev1.Pod{}, fmt.Errorf("kubernetes clientset is required")
	}
	namespace := a.options.Client.Namespace
	if entry.Namespace != "" {
		pod, err := a.options.Client.Clientset.CoreV1().Pods(entry.Namespace).Get(ctx, entry.Pod, metav1.GetOptions{})
		if err != nil {
			return corev1.Pod{}, err
		}
		return *pod, nil
	}
	if entry.Pod != "" {
		targets, err := a.selectedLogTargets(ctx)
		if err == nil {
			for _, target := range targets {
				if target.Pod == entry.Pod && target.Namespace != "" {
					namespace = target.Namespace
					break
				}
			}
		}
	}
	pod, err := a.options.Client.Clientset.CoreV1().Pods(namespace).Get(ctx, entry.Pod, metav1.GetOptions{})
	if err != nil {
		return corev1.Pod{}, err
	}
	return *pod, nil
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
	list.AddItem("All containers", "Includes init and ephemeral containers", 0, func() { a.showLogsView(logs.AllTargetsForPods([]corev1.Pod{pod})) })
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.showLogsView([]logs.PodLogTarget{logTargetForPodContainer(pod, container)})
		})
	}
	list.SetTitle(" Select log container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyRune && event.Rune() == 'q' {
			a.showMain()
			return nil
		}
		return event
	})
	a.app.SetRoot(list, true)
}

func logTargetForPodContainer(pod corev1.Pod, container string) logs.PodLogTarget {
	return logs.PodLogTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: container, PodUID: pod.UID}
}

func toggleLogWrap(view *tview.TextView, wrap *bool) {
	*wrap = !*wrap
	view.SetWrap(*wrap)
}

func (a *App) showLogFilter(view *tview.TextView, buffer *logs.Buffer, filter *string, render func()) {
	input := tview.NewInputField().
		SetLabel("Log filter: ").
		SetText(*filter).
		SetFieldWidth(50)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			*filter = input.GetText()
			render()
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
					view.SetTitle(" Logs saved to " + tview.Escape(path) + " ")
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
	output := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	output.SetTitle(" AI log summary · Esc back ").SetBorder(true)
	output.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.app.SetRoot(view, true)
			return nil
		}
		return e
	})
	a.app.SetRoot(output, true)
	a.streamAIResponse(output, ai.LogSummaryRequest(prompt))
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
	output := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetText("Ask a follow-up about these logs.")
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
		if event.Key() == tcell.KeyEsc {
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
	colored := fmt.Sprintf("[%s]%s[-]", logColor(entry.Color), tview.Escape(logs.PrefixPod(entry.Source(), entry.Line)))
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
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, logs.FormatEntry(entry, true))
	}
	return strings.Join(lines, "\n")
}

type logLineWriter struct {
	pod       string
	target    logs.PodLogTarget
	buffer    *logs.Buffer
	onAppend  func()
	pending   string
	truncated bool
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

const maxLogLineBytes = 256 * 1024

func (w *logLineWriter) Write(data []byte) (int, error) {
	size := len(data)
	for len(data) > 0 {
		end := -1
		for i, b := range data {
			if b == '\n' {
				end = i
				break
			}
		}
		part := data
		if end >= 0 {
			part = data[:end]
		}
		remaining := maxLogLineBytes - len(w.pending)
		if len(part) > remaining {
			w.truncated = true
			part = part[:remaining]
		}
		w.pending += string(part)
		if end < 0 {
			break
		}
		w.emit()
		data = data[end+1:]
	}
	return size, nil
}

func (w *logLineWriter) emit() {
	line := strings.TrimSuffix(w.pending, "\r")
	if w.truncated {
		line += " … [line truncated]"
	}
	stamp := time.Now()
	if first, rest, ok := strings.Cut(line, " "); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, first); err == nil {
			stamp = parsed
			line = rest
		}
	}
	target := w.target
	if target.Pod == "" {
		target.Pod = w.pod
	}
	w.buffer.AppendSource(target, line, stamp)
	w.pending, w.truncated = "", false
	if w.onAppend != nil {
		w.onAppend()
	}
}

func (w *logLineWriter) Flush() {
	if w.pending != "" || w.truncated {
		w.emit()
	}
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
	default:
		return nil, fmt.Errorf("logs are currently implemented for pod and workload selections")
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
	return logs.TargetsForPods(pods), nil
}

func logTargetsForObject(ctx context.Context, client *cluster.Client, obj *unstructured.Unstructured) ([]logs.PodLogTarget, error) {
	if obj.GetKind() == "Pod" {
		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod); err != nil {
			return nil, err
		}
		return logs.TargetsForPods([]corev1.Pod{pod}), nil
	}
	raw, _, _ := unstructured.NestedMap(obj.Object, "spec", "selector")
	var selector metav1.LabelSelector
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &selector); err != nil {
		return nil, err
	}
	s, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil || s.Empty() {
		return nil, fmt.Errorf("resource has no pod selector")
	}
	pods, err := logs.PodsForSelector(ctx, client.Clientset, obj.GetNamespace(), s)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no matching pods")
	}
	return logs.AllTargetsForPods(pods), nil
}
func logDocument(view *tview.TextView, entries []logs.Entry, wrap bool) (string, []logs.Entry) {
	_, _, width, _ := view.GetInnerRect()
	if width < 1 {
		width = 120
	}
	var lines []string
	var rows []logs.Entry
	for _, e := range entries {
		text := formatLogEntryForView(e, true)
		parts := []string{text}
		if wrap {
			parts = tview.WordWrap(text, width)
		}
		for _, part := range parts {
			lines = append(lines, part)
			rows = append(rows, e)
		}
	}
	return strings.Join(lines, "\n"), rows
}
func (a *App) currentLogEntry(view *tview.TextView, buffer *logs.Buffer, filter string) (logs.Entry, bool) {
	row, _ := view.GetScrollOffset()
	if len(a.logRows) > 0 {
		return a.logRows[min(max(row, 0), len(a.logRows)-1)], true
	}
	return selectedLogEntry(view, buffer, filter)
}
