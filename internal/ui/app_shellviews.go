//revive:disable:exported package-comments
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// --- Helm ---

type helmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

// parseHelmList parses `helm list -A -o json` output.
func parseHelmList(data []byte) ([]helmRelease, error) {
	var releases []helmRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, err
	}
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Namespace != releases[j].Namespace {
			return releases[i].Namespace < releases[j].Namespace
		}
		return releases[i].Name < releases[j].Name
	})
	return releases, nil
}

// showHelmView lists Helm releases via the helm CLI.
func (a *App) showHelmView() {
	if _, err := exec.LookPath("helm"); err != nil {
		a.showMessage("Helm unavailable", "helm not found in PATH. Install helm to use :helm.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "helm", "list", "-A", "-o", "json").Output() // #nosec G204 -- fixed args.
	if err != nil {
		a.Flash(FlashError, "helm list failed: %s", err.Error())
		return
	}
	releases, err := parseHelmList(out)
	if err != nil {
		a.Flash(FlashError, "helm list parse: %s", err.Error())
		return
	}
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(" helm releases ").SetBorder(true)
	for col, h := range []string{"NAMESPACE", "NAME", "REVISION", "STATUS", "CHART", "APP VERSION"} {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	if len(releases) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("no releases"))
	}
	for r, rel := range releases {
		values := []string{rel.Namespace, rel.Name, rel.Revision, rel.Status, rel.Chart, rel.AppVersion}
		for c, v := range values {
			cell := tview.NewTableCell(v)
			if c == 3 && rel.Status != "deployed" {
				cell.SetTextColor(a.semanticColor("warn"))
			}
			table.SetCell(r+1, c, cell)
		}
	}
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Rune() == 'q':
			a.back()
			return nil
		case event.Rune() == 'd' || event.Key() == tcell.KeyEnter:
			row, _ := table.GetSelection()
			idx := row - 1
			if idx >= 0 && idx < len(releases) {
				a.showHelmHistory(releases[idx])
			}
			return nil
		}
		return event
	})
	a.pushView(table, table, "helm", nil)
}

func (a *App) showHelmHistory(rel helmRelease) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "helm", "history", rel.Name, "-n", rel.Namespace).CombinedOutput() // #nosec G204 -- values from helm output.
	body := string(out)
	if err != nil {
		body = "helm history failed: " + err.Error() + "\n\n" + body
	}
	view := tview.NewTextView().SetScrollable(true).SetText(body)
	view.SetTitle(" helm history: " + rel.Name + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	a.pushView(view, view, "helm:"+rel.Name, nil)
}

// --- Popeye ---

type popeyeReport struct {
	Popeye struct {
		Score      int    `json:"score"`
		Grade      string `json:"grade"`
		Sanitizers []struct {
			Sanitizer string `json:"sanitizer"`
			Tally     struct {
				OK      int `json:"ok"`
				Info    int `json:"info"`
				Warning int `json:"warning"`
				Error   int `json:"error"`
			} `json:"tally"`
		} `json:"sanitizers"`
	} `json:"popeye"`
}

type popeyeRow struct {
	Sanitizer            string
	OK, Info, Warn, Errs int
}

// parsePopeye parses `popeye --out json` into per-sanitizer rows + overall grade.
func parsePopeye(data []byte) ([]popeyeRow, string, int, error) {
	var report popeyeReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, "", 0, err
	}
	rows := make([]popeyeRow, 0, len(report.Popeye.Sanitizers))
	for _, s := range report.Popeye.Sanitizers {
		rows = append(rows, popeyeRow{
			Sanitizer: s.Sanitizer,
			OK:        s.Tally.OK,
			Info:      s.Tally.Info,
			Warn:      s.Tally.Warning,
			Errs:      s.Tally.Error,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Sanitizer < rows[j].Sanitizer })
	return rows, report.Popeye.Grade, report.Popeye.Score, nil
}

// showPopeyeView runs a popeye scan and renders the sanitizer scores.
func (a *App) showPopeyeView() {
	if _, err := exec.LookPath("popeye"); err != nil {
		a.showMessage("Popeye unavailable", "popeye not found in PATH. Install popeye to use :popeye.")
		return
	}
	a.Flash(FlashInfo, "running popeye…")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "popeye", "--out", "json").Output() // #nosec G204 -- fixed args.
	if err != nil && len(out) == 0 {
		a.Flash(FlashError, "popeye failed: %s", err.Error())
		return
	}
	rows, grade, score, perr := parsePopeye(out)
	if perr != nil {
		a.Flash(FlashError, "popeye parse: %s", perr.Error())
		return
	}
	table := tview.NewTable().SetSelectable(true, false)
	table.SetTitle(fmt.Sprintf(" popeye  grade=%s score=%d ", grade, score)).SetBorder(true)
	for col, h := range []string{"SANITIZER", "OK", "INFO", "WARN", "ERROR"} {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	for r, row := range rows {
		table.SetCell(r+1, 0, tview.NewTableCell(row.Sanitizer))
		table.SetCell(r+1, 1, tview.NewTableCell(fmt.Sprint(row.OK)))
		table.SetCell(r+1, 2, tview.NewTableCell(fmt.Sprint(row.Info)))
		warn := tview.NewTableCell(fmt.Sprint(row.Warn))
		if row.Warn > 0 {
			warn.SetTextColor(a.semanticColor("warn"))
		}
		table.SetCell(r+1, 3, warn)
		errs := tview.NewTableCell(fmt.Sprint(row.Errs))
		if row.Errs > 0 {
			errs.SetTextColor(a.semanticColor("error"))
		}
		table.SetCell(r+1, 4, errs)
	}
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	a.pushView(table, table, "popeye", nil)
}
