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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sort"
	"strings"
	"time"
)

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
	a.showLogsView(targets)
}

// logViewState holds the mutable streaming options for a log view.
type logViewState struct {
	previous     bool
	timestamps   bool
	autoscroll   bool
	pretty       bool
	sinceSeconds int64 // 0 = all history
	tail         int64
}

func (s *logViewState) options() logs.RequestOptions {
	opts := logs.RequestOptions{Follow: true, Previous: s.previous, Timestamps: s.timestamps}
	if s.tail > 0 {
		tail := s.tail
		opts.TailLines = &tail
	}
	if s.sinceSeconds > 0 {
		since := s.sinceSeconds
		opts.SinceSeconds = &since
	}
	return opts
}

func (s *logViewState) title() string {
	scroll := "follow"
	if !s.autoscroll {
		scroll = "paused"
	}
	since := "all"
	if s.sinceSeconds > 0 {
		since = (time.Duration(s.sinceSeconds) * time.Second).String()
	}
	extra := ""
	if s.previous {
		extra += " prev"
	}
	if s.timestamps {
		extra += " ts"
	}
	if s.pretty {
		extra += " json"
	}
	return fmt.Sprintf(" Logs [%s since=%s%s] (p prev · t ts · 1/2/3/4/0 since · ^S scroll · j json · C clear) ", scroll, since, extra)
}

func (a *App) showLogsView(targets []logs.PodLogTarget) {
	buffer := logs.NewBuffer(10_000)
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	wrap := true
	view.SetWrap(wrap).SetWordWrap(true)
	filter := ""
	search := logSearchState{}
	gotoPending := false
	st := &logViewState{autoscroll: true, tail: a.defaultTailLines(), sinceSeconds: a.defaultSinceSeconds()}
	var cancel context.CancelFunc
	streamer := a.logStreamer()

	render := func() {
		view.SetText(renderLogView(buffer.Filter(filter), st.pretty))
		if st.autoscroll {
			view.ScrollToEnd()
		}
	}
	launch := func(clearBuf bool) {
		if cancel != nil {
			cancel()
		}
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		if clearBuf {
			buffer.Reset()
		}
		opts := st.options()
		for _, target := range targets {
			target := target
			streamCtx := ctx
			go func() {
				writer := newLogLineWriter(target.Pod, buffer, func() {
					a.app.QueueUpdateDraw(render)
				})
				if err := streamer(streamCtx, target, opts, writer); err != nil && streamCtx.Err() == nil {
					buffer.Append(target.Pod, "stream error: "+err.Error(), time.Now())
					a.app.QueueUpdateDraw(render)
				}
			}()
		}
		view.SetTitle(st.title())
		render()
	}
	view.SetBorder(true)
	launch(false)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Rune() == 'q':
			a.back()
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
		case event.Rune() == 'p':
			st.previous = !st.previous
			launch(true)
			return nil
		case event.Rune() == 't':
			st.timestamps = !st.timestamps
			launch(true)
			return nil
		case event.Rune() == 'j':
			st.pretty = !st.pretty
			view.SetTitle(st.title())
			render()
			return nil
		case event.Key() == tcell.KeyCtrlS:
			st.autoscroll = !st.autoscroll
			view.SetTitle(st.title())
			return nil
		case event.Rune() == 'C':
			buffer.Reset()
			render()
			return nil
		case event.Rune() == '1':
			st.sinceSeconds = 60
			launch(true)
			return nil
		case event.Rune() == '2':
			st.sinceSeconds = 300
			launch(true)
			return nil
		case event.Rune() == '3':
			st.sinceSeconds = 900
			launch(true)
			return nil
		case event.Rune() == '4':
			st.sinceSeconds = 3600
			launch(true)
			return nil
		case event.Rune() == '0':
			st.sinceSeconds = 0
			launch(true)
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
	a.pushView(view, view, "logs", func() {
		if cancel != nil {
			cancel()
		}
	})
}

func (a *App) jumpLogEvents(view *tview.TextView, buffer *logs.Buffer, filter string) {
	entry, ok := selectedLogEntry(view, buffer, filter)
	if !ok || strings.TrimSpace(entry.Pod) == "" {
		view.SetText("No pod log line selected.")
		return
	}
	a.switchResourceFiltered(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "events"}, entry.Pod)
	a.back()
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
	a.back()
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
	for _, container := range logs.ContainerNames(pod) {
		container := container
		list.AddItem(container, "", 0, func() {
			a.closeModal()
			a.showLogsView([]logs.PodLogTarget{logTargetForPodContainer(pod, container)})
		})
	}
	list.SetTitle(" Select log container ").SetBorder(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.closeModal()
			return nil
		}
		return event
	})
	a.showModal(list)
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
		a.closeModal()
	})
	a.showModal(input)
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
		a.closeModal()
	})
	a.showModal(input)
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
		a.closeModal()
	})
	a.showModal(input)
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
	output := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetText("Ask a follow-up about these logs.")
	output.SetTitle(" AI logs ").SetBorder(true)
	input := tview.NewInputField().SetLabel("Ask: ").SetFieldWidth(0)
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			question := strings.TrimSpace(input.GetText())
			if question == "" {
				return
			}
			input.SetText("")
			a.streamAIResponse(output, ai.LogQuestionRequest(contextText, question))
		}
	})
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(output, 0, 1, false).
		AddItem(input, 1, 0, true)
	a.pushView(flex, input, "ai:logs", nil)
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

// renderLogView renders entries either raw (with per-pod color) or, in pretty
// mode, as aligned parsed fields with severity-colored levels.
func renderLogView(entries []logs.Entry, pretty bool) string {
	if !pretty {
		return renderLogEntries(entries, true)
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, formatLogEntryPretty(entry))
	}
	return strings.Join(lines, "\n")
}

func formatLogEntryPretty(entry logs.Entry) string {
	parsed := entry.Parse
	if parsed.Format == "text" || len(parsed.Fields) == 0 {
		return formatLogEntryForView(entry, true)
	}
	level := firstNonEmpty(parsed.Fields["level"], parsed.Fields["severity"], parsed.Fields["lvl"])
	msg := firstNonEmpty(parsed.Fields["msg"], parsed.Fields["message"])
	prefix := ""
	if entry.Pod != "" {
		prefix = fmt.Sprintf("[%s]%s[-] ", logColor(entry.Color), entry.Pod)
	}
	levelTag := ""
	if level != "" {
		levelTag = fmt.Sprintf("[%s]%-5s[-] ", severityColor(level), strings.ToUpper(level))
	}
	extras := make([]string, 0, len(parsed.Fields))
	keys := make([]string, 0, len(parsed.Fields))
	for k := range parsed.Fields {
		switch k {
		case "level", "severity", "lvl", "msg", "message":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		extras = append(extras, fmt.Sprintf("%s=%s", k, parsed.Fields[k]))
	}
	line := prefix + levelTag + msg
	if len(extras) > 0 {
		line += "  [gray]" + strings.Join(extras, " ") + "[-]"
	}
	return line
}

func severityColor(level string) string {
	switch strings.ToLower(level) {
	case "error", "err", "fatal", "panic", "crit":
		return "red"
	case "warn", "warning":
		return "yellow"
	case "debug", "trace":
		return "gray"
	default:
		return "green"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (a *App) defaultTailLines() int64 {
	if a.options.Config.Logs.TailLines > 0 {
		return a.options.Config.Logs.TailLines
	}
	return 200
}

func (a *App) defaultSinceSeconds() int64 {
	return a.options.Config.Logs.SinceSeconds
}

func formatLogEntryForView(entry logs.Entry, prefixPod bool) string {
	if !prefixPod || entry.Pod == "" {
		return logs.FormatEntry(entry, prefixPod)
	}
	colored := fmt.Sprintf("[%s]%s[-]", logColor(entry.Color), logs.PrefixPod(entry.Pod, entry.Line))
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
	pod      string
	buffer   *logs.Buffer
	onAppend func()
	pending  string
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
		if w.onAppend != nil {
			w.onAppend()
		}
	}
	return len(data), scanner.Err()
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
