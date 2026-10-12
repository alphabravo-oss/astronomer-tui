package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/integrations"
	"github.com/gdamore/tcell/v2"
)

func (a *App) showHelm() {
	client := a.options.Client
	ns := client.Namespace
	a.operation("List Helm releases", func(ctx context.Context) (func(), error) {
		releases, err := integrations.ListReleases(ctx, client, ns)
		if err != nil {
			return nil, err
		}
		return func() {
			t := a.explorerTable("Helm · Enter history · v values · m manifest · Ctrl-D uninstall · r refresh · Esc back", []string{"NAMESPACE", "NAME", "REVISION", "STATUS", "CHART"})
			for i, r := range releases {
				a.setExplorerRow(t, i+1, r.Namespace, r.Name, r.Revision, r.Status, r.Chart)
			}
			if len(releases) == 0 {
				a.setExplorerRow(t, 1, "No releases")
			}
			if len(releases) == 1000 {
				t.SetTitle(" Helm · first 1000 releases · narrow namespace to see more ")
			}
			selected := func() (integrations.Release, bool) {
				row, _ := t.GetSelection()
				if row < 1 || row > len(releases) {
					return integrations.Release{}, false
				}
				return releases[row-1], true
			}
			t.SetSelectedFunc(func(int, int) {
				if r, ok := selected(); ok {
					a.showHelmHistory(r)
				}
			})
			t.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEsc {
					a.showMain()
					return nil
				}
				if e.Key() == tcell.KeyRune && e.Rune() == 'r' {
					a.showHelm()
					return nil
				}
				r, ok := selected()
				if !ok {
					return e
				}
				if e.Key() == tcell.KeyRune && (e.Rune() == 'v' || e.Rune() == 'm') {
					what := "values"
					if e.Rune() == 'm' {
						what = "manifest"
					}
					a.showHelmDocument(r, what, r.Revision)
					return nil
				}
				if e.Key() == tcell.KeyCtrlD {
					a.confirmExternalMutation("Helm uninstall", fmt.Sprintf("Uninstall release %s/%s at revision %s?", r.Namespace, r.Name, r.Revision), func() {
						a.operationWithTimeout("Uninstall Helm release", 3*time.Minute, func(ctx context.Context) (func(), error) {
							if err := integrations.CheckRevision(ctx, client, r); err != nil {
								return nil, err
							}
							data, err := integrations.Helm(ctx, client, r.Namespace, "uninstall", r.Name, "--wait", "--timeout", "120s")
							return func() { a.showDocument("Helm uninstall", string(data)) }, err
						})
					})
					return nil
				}
				return e
			})
			a.app.SetRoot(t, true).SetFocus(t)
		}, nil
	})
}
func (a *App) showHelmDocument(r integrations.Release, what, revision string) {
	client := a.options.Client
	a.operation("Helm "+what, func(ctx context.Context) (func(), error) {
		args := []string{"get", what, r.Name, "--revision", revision}
		if what == "values" {
			args = append(args, "--all")
		}
		data, err := integrations.Helm(ctx, client, r.Namespace, args...)
		return func() { a.showDocument("Helm "+r.Namespace+"/"+r.Name+" · "+what+" · "+revision, string(data)) }, err
	})
}
func (a *App) showHelmHistory(r integrations.Release) {
	client := a.options.Client
	a.operation("Helm history", func(ctx context.Context) (func(), error) {
		history, err := integrations.History(ctx, client, r)
		if err != nil {
			return nil, err
		}
		return func() {
			t := a.explorerTable("Helm history · Enter manifest · v values · r rollback · Esc back", []string{"REVISION", "STATUS", "CHART", "DESCRIPTION"})
			for i, h := range history {
				a.setExplorerRow(t, i+1, strconv.Itoa(h.Revision), h.Status, h.Chart, h.Description)
			}
			revision := func() string {
				row, _ := t.GetSelection()
				if row < 1 || row > len(history) {
					return ""
				}
				return strconv.Itoa(history[row-1].Revision)
			}
			t.SetSelectedFunc(func(int, int) {
				if v := revision(); v != "" {
					a.showHelmDocument(r, "manifest", v)
				}
			})
			t.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEsc {
					a.showHelm()
					return nil
				}
				v := revision()
				if v == "" {
					return e
				}
				if e.Key() == tcell.KeyRune && e.Rune() == 'v' {
					a.showHelmDocument(r, "values", v)
					return nil
				}
				if e.Key() == tcell.KeyRune && e.Rune() == 'r' {
					a.confirmExternalMutation("Helm rollback", fmt.Sprintf("Roll back %s/%s from revision %s to %s?", r.Namespace, r.Name, r.Revision, v), func() {
						a.operationWithTimeout("Helm rollback", 3*time.Minute, func(ctx context.Context) (func(), error) {
							if err := integrations.CheckRevision(ctx, client, r); err != nil {
								return nil, err
							}
							data, err := integrations.Helm(ctx, client, r.Namespace, "rollback", r.Name, v, "--wait", "--timeout", "120s")
							return func() { a.showDocument("Helm rollback", string(data)) }, err
						})
					})
					return nil
				}
				return e
			})
			a.app.SetRoot(t, true).SetFocus(t)
		}, nil
	})
}
