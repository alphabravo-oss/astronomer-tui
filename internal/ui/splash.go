//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// splashDuration is how long the startup banner lingers before it auto-dismisses
// to the resource table. Any keypress dismisses it immediately.
const splashDuration = 1800 * time.Millisecond

// showSplash renders the astrotui startup banner as the initial root, then hands
// off to the resource table on the first keypress or after splashDuration.
func (a *App) showSplash(ctx context.Context) {
	dismiss := a.splashDismisser()
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText(a.splashText())
	view.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	view.SetInputCapture(func(*tcell.EventKey) *tcell.EventKey {
		dismiss()
		return nil
	})
	a.app.SetRoot(view, true).SetFocus(view)
	go func() {
		select {
		case <-time.After(splashDuration):
			a.queueUpdate(ctx, dismiss)
		case <-ctx.Done():
		}
	}()
}

// splashDismisser returns a func that swaps the root to the table exactly once,
// so the keypress and timer paths can't double-fire.
func (a *App) splashDismisser() func() {
	var once sync.Once
	return func() {
		once.Do(func() { a.showMain() })
	}
}

func (a *App) splashText() string {
	accent := tagColor(a.skin.Accent, "aqua")
	fg := tagColor(a.skin.FG, "white")
	subtle := tagColor(a.skin.Subtle, "gray")
	version := strings.TrimSpace(a.options.Version)
	lines := []string{
		"",
		"",
		fmt.Sprintf("[%s::b]%s[-:-:-]", accent, astroLogo),
		fmt.Sprintf("[%s]by AlphaBravo[-:-:-]", fg),
		"",
		fmt.Sprintf("[%s]Astro-TUI · A Kubernetes terminal UI[-:-:-]", fg),
		fmt.Sprintf("[%s]%s[-:-:-]", subtle, tview.Escape(version)),
		"",
		fmt.Sprintf("[%s]press any key to continue[-:-:-]", subtle),
	}
	return strings.Join(lines, "\n")
}

// tagColor returns a color name/hex usable inside a tview color tag, falling back
// when the skin leaves the value blank.
func tagColor(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
