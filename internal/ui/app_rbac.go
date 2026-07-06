//revive:disable:exported package-comments
package ui

import (
	"context"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/alphabravo-oss/astrotui/internal/actions"
)

// showRBACView resolves a subject's aggregated policy rules into a table.
func (a *App) showRBACView(_ context.Context, subjectSpec string) {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		a.Flash(FlashError, "kubernetes clientset is required")
		return
	}
	subject, err := actions.ParseSubject(subjectSpec)
	if err != nil {
		a.Flash(FlashError, "rbac: %s", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rules, err := actions.ResolveSubjectRules(ctx, a.options.Client.Clientset, subject)
	if err != nil {
		a.Flash(FlashError, "rbac: %s", err.Error())
		return
	}
	table := tview.NewTable().SetSelectable(true, false)
	title := " rbac " + subject.Kind + "/" + subject.Name
	if subject.Namespace != "" {
		title = " rbac " + subject.Kind + "/" + subject.Namespace + "/" + subject.Name
	}
	table.SetTitle(title + " ").SetBorder(true)
	for col, h := range []string{"SCOPE", "APIGROUPS", "RESOURCES", "VERBS", "VIA"} {
		table.SetCell(0, col, tview.NewTableCell(h).SetTextColor(a.semanticColor("accent")).SetSelectable(false))
	}
	if len(rules) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("no rules found for subject"))
	}
	for r, rule := range rules {
		values := []string{rule.Scope, rule.APIGroups, rule.Resources, rule.Verbs, rule.Via}
		for c, v := range values {
			table.SetCell(r+1, c, tview.NewTableCell(v))
		}
	}
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	a.pushView(table, table, "rbac:"+subject.Name, nil)
}

// runCanI runs a SelfSubjectAccessReview and flashes the result.
func (a *App) runCanI(_ context.Context, verbResource string) {
	if a.options.Client == nil || a.options.Client.Clientset == nil {
		a.Flash(FlashError, "kubernetes clientset is required")
		return
	}
	parts := strings.Fields(verbResource)
	if len(parts) < 2 {
		a.Flash(FlashError, "usage: :can <verb> <resource>")
		return
	}
	namespace := ""
	if a.options.Client != nil {
		namespace = a.options.Client.Namespace
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	allowed, reason, err := actions.CanI(ctx, a.options.Client.Clientset, parts[0], parts[1], namespace)
	if err != nil {
		a.Flash(FlashError, "can-i: %s", err.Error())
		return
	}
	if allowed {
		a.Flash(FlashInfo, "yes: %s %s is allowed", parts[0], parts[1])
		return
	}
	msg := "no: " + parts[0] + " " + parts[1] + " is denied"
	if reason != "" {
		msg += " (" + reason + ")"
	}
	a.Flash(FlashWarn, "%s", msg)
}
