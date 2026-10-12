package ui

import (
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) previewAI(req ai.Request, send func(ai.Request), back func()) {
	prepared, err := ai.Prepare(req)
	if err != nil {
		a.showMessage("AI request blocked", err.Error())
		return
	}
	data, _ := json.MarshalIndent(prepared, "", "  ")
	provider := a.options.Config.AI.Provider
	if provider == "" {
		provider = "configured provider"
	}
	text := tview.NewTextView().SetScrollable(true).SetText(fmt.Sprintf("Destination: %s %s\nReview the exact outgoing content below. Redaction cannot detect every secret in arbitrary text.\n\n%s", provider, a.options.Config.AI.Endpoint, data))
	text.SetTitle(" Review AI request · Tab buttons · Esc cancel ").SetBorder(true)
	form := tview.NewForm().AddButton("Cancel", back).AddButton("Send", func() { send(prepared) }).SetButtonsAlign(tview.AlignCenter)
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(text, 0, 1, true).AddItem(form, 3, 0, false)
	flex.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			back()
			return nil
		}
		if e.Key() == tcell.KeyTab {
			if a.app.GetFocus() == text {
				a.app.SetFocus(form)
			} else {
				a.app.SetFocus(text)
			}
			return nil
		}
		return e
	})
	a.app.SetRoot(flex, true).SetFocus(text)
}
