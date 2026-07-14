package ui

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/alphabravo-oss/astrotui/internal/actions"
)

func (a *App) benchmarkManager() *actions.BenchmarkManager {
	if a.options.Benchmarks == nil {
		a.options.Benchmarks = actions.NewBenchmarkManager()
	}
	return a.options.Benchmarks
}

func (a *App) showBenchmarkForm(forward actions.PortForward) {
	pathInput := tview.NewInputField().SetLabel("HTTP path: ").SetText("/").SetFieldWidth(40)
	requestInput := tview.NewInputField().SetLabel("Requests: ").SetText("100").SetFieldWidth(8)
	concurrencyInput := tview.NewInputField().SetLabel("Concurrency: ").SetText("10").SetFieldWidth(8)
	timeoutInput := tview.NewInputField().SetLabel("Timeout: ").SetText("30s").SetFieldWidth(10)
	form := tview.NewForm().
		AddFormItem(pathInput).
		AddFormItem(requestInput).
		AddFormItem(concurrencyInput).
		AddFormItem(timeoutInput)
	form.AddButton("Run", func() {
		spec, err := benchmarkSpecFromInputs(forward, pathInput.GetText(), requestInput.GetText(), concurrencyInput.GetText(), timeoutInput.GetText())
		if err != nil {
			a.showMessage("Benchmark unavailable", err.Error())
			return
		}
		id, err := a.benchmarkManager().Start(a.context(), spec, a.options.BenchmarkRunner)
		if err != nil {
			a.showMessage("Benchmark unavailable", err.Error())
			return
		}
		a.showMessage("Benchmark started", id+" "+spec.URL)
	})
	form.AddButton("Cancel", func() { a.app.SetRoot(a.table, true) })
	form.SetTitle(" Benchmark " + forward.Resource + " ").SetBorder(true)
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.app.SetRoot(a.table, true)
			return nil
		}
		return event
	})
	a.app.SetRoot(form, true)
}

func benchmarkSpecFromInputs(forward actions.PortForward, rawPath, rawRequests, rawConcurrency, rawTimeout string) (actions.BenchmarkSpec, error) {
	reference, err := url.Parse(strings.TrimSpace(rawPath))
	if err != nil || reference.IsAbs() || reference.Host != "" || !strings.HasPrefix(reference.Path, "/") {
		return actions.BenchmarkSpec{}, fmt.Errorf("HTTP path must start with / and cannot specify a host")
	}
	requests, err := strconv.Atoi(strings.TrimSpace(rawRequests))
	if err != nil {
		return actions.BenchmarkSpec{}, fmt.Errorf("requests must be an integer")
	}
	concurrency, err := strconv.Atoi(strings.TrimSpace(rawConcurrency))
	if err != nil {
		return actions.BenchmarkSpec{}, fmt.Errorf("concurrency must be an integer")
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(rawTimeout))
	if err != nil {
		return actions.BenchmarkSpec{}, fmt.Errorf("timeout must be a duration such as 30s")
	}
	spec := actions.DefaultBenchmarkSpec(forward)
	base, _ := url.Parse(spec.URL)
	spec.URL = base.ResolveReference(reference).String()
	spec.Requests = requests
	spec.Concurrency = concurrency
	spec.Timeout = timeout
	if err := actions.ValidateBenchmarkSpec(spec); err != nil {
		return actions.BenchmarkSpec{}, err
	}
	return spec, nil
}

func (a *App) showBenchmarkManager() {
	manager := a.benchmarkManager()
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" Benchmarks (enter details, k cancel) ").SetBorder(true)
	renderBenchmarkRows(table, manager.List())
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			a.app.SetRoot(a.table, true)
			return nil
		case event.Key() == tcell.KeyCtrlK || event.Rune() == 'k':
			if run, ok := benchmarkAt(table, manager.List()); ok {
				manager.Cancel(run.ID)
				renderBenchmarkRows(table, manager.List())
			}
			return nil
		case event.Key() == tcell.KeyEnter:
			if run, ok := benchmarkAt(table, manager.List()); ok {
				a.showScrollableText(" Benchmark "+run.ID+" ", benchmarkDetails(run))
			}
			return nil
		}
		return event
	})
	a.app.SetRoot(table, true)
}

func renderBenchmarkRows(table *tview.Table, runs []actions.Benchmark) {
	table.Clear()
	headers := []string{"ID", "STATUS", "URL", "DONE", "FAILED", "P50", "P95", "ELAPSED"}
	for col, header := range headers {
		table.SetCell(0, col, tview.NewTableCell(header).SetTextColor(tcell.ColorAqua).SetSelectable(false))
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	for row, run := range runs {
		values := []string{
			run.ID, run.Status, run.Spec.URL, fmt.Sprint(run.Result.Completed), fmt.Sprint(run.Result.Failed),
			run.Result.Median.Round(time.Microsecond).String(), run.Result.P95.Round(time.Microsecond).String(), run.Result.Elapsed.Round(time.Millisecond).String(),
		}
		for col, value := range values {
			table.SetCell(row+1, col, tview.NewTableCell(value))
		}
	}
	if len(runs) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("No benchmark runs"))
	}
}

func benchmarkAt(table *tview.Table, runs []actions.Benchmark) (actions.Benchmark, bool) {
	row, _ := table.GetSelection()
	index := row - 1
	if index < 0 || index >= len(runs) {
		return actions.Benchmark{}, false
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return runs[index], true
}

func benchmarkDetails(run actions.Benchmark) string {
	codes := make([]int, 0, len(run.Result.StatusCodes))
	for code := range run.Result.StatusCodes {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	status := make([]string, 0, len(codes))
	for _, code := range codes {
		status = append(status, fmt.Sprintf("%d=%d", code, run.Result.StatusCodes[code]))
	}
	return fmt.Sprintf("URL: %s\nStatus: %s\nRequests: %d\nConcurrency: %d\nCompleted: %d\nFailed: %d\nHTTP status: %s\nMin: %s\nMedian: %s\nP95: %s\nMax: %s\nElapsed: %s\nError: %s",
		run.Spec.URL, run.Status, run.Spec.Requests, run.Spec.Concurrency, run.Result.Completed, run.Result.Failed, strings.Join(status, ", "),
		run.Result.Min, run.Result.Median, run.Result.P95, run.Result.Max, run.Result.Elapsed, run.Result.Error)
}
