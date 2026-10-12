package ui

import (
	"context"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"time"
)

// operation runs bounded work against a captured target. Only its completion
// callback can access UI state. Escape cancels and invalidates late results.
func (a *App) operation(title string, work func(context.Context) (func(), error)) {
	a.operationWithTimeout(title, 30*time.Second, work)
}

func (a *App) operationWithTimeout(title string, timeout time.Duration, work func(context.Context) (func(), error)) {
	a.stopAuxiliary()
	if a.operationCancel != nil {
		a.operationCancel()
	}
	a.operationID++
	id := a.operationID
	ctx, cancel := context.WithTimeout(a.backgroundContext(), timeout)
	a.operationCancel = cancel
	view := tview.NewTextView().SetText(title + "…\nEsc cancels")
	view.SetBorder(true)
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			cancel()
			a.operationID++
			a.showMain()
			return nil
		}
		return e
	})
	a.app.SetRoot(view, true)
	go func() {
		finish, err := work(ctx)
		a.queueUpdate(a.backgroundContext(), func() {
			defer cancel()
			if id != a.operationID {
				return
			}
			if ctx.Err() != nil {
				a.showMessage(title, ctx.Err().Error())
				return
			}
			if err != nil {
				a.showMessage(title, err.Error())
				return
			}
			if finish != nil {
				finish()
			}
		})
	}()
}
