//revive:disable:exported package-comments
package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

// FlashLevel classifies a transient status message.
type FlashLevel int

const (
	// FlashInfo is a neutral informational message.
	FlashInfo FlashLevel = iota
	// FlashWarn signals a recoverable problem.
	FlashWarn
	// FlashError signals a failure the user should notice.
	FlashError
)

const flashTTL = 5 * time.Second

// flashHistory keeps a bounded ring of recent messages for the :flash view.
type flashEntry struct {
	level FlashLevel
	text  string
	at    time.Time
}

var (
	flashMu      sync.Mutex
	flashHistory []flashEntry
)

// Flash shows a transient message in the bottom status line and records it in
// history. Safe to call from any goroutine.
func (a *App) Flash(level FlashLevel, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	recordFlash(level, msg)
	if a.app == nil || a.flashView == nil {
		return
	}
	a.app.QueueUpdateDraw(func() { a.renderFlash(level, msg) })
}

func (a *App) renderFlash(level FlashLevel, msg string) {
	color := a.flashColor(level)
	a.flashView.SetText(fmt.Sprintf(" [%s]%s[-:-:-]", colorName(color), msg))
	if a.flashClear != nil {
		a.flashClear.Stop()
	}
	a.flashClear = time.AfterFunc(flashTTL, func() {
		a.app.QueueUpdateDraw(func() { a.flashView.SetText("") })
	})
}

func (a *App) flashColor(level FlashLevel) tcell.Color {
	switch level {
	case FlashError:
		return skinColor(a.skin.Error, tcell.ColorRed)
	case FlashWarn:
		return skinColor(a.skin.Warn, tcell.ColorYellow)
	default:
		return skinColor(a.skin.Accent, tcell.ColorAqua)
	}
}

func colorName(c tcell.Color) string {
	return fmt.Sprintf("#%06x", c.Hex())
}

func recordFlash(level FlashLevel, msg string) {
	flashMu.Lock()
	defer flashMu.Unlock()
	flashHistory = append(flashHistory, flashEntry{level: level, text: msg, at: time.Now()})
	if len(flashHistory) > 50 {
		flashHistory = flashHistory[len(flashHistory)-50:]
	}
}

// flashLog returns the recorded flash history, newest last.
func flashLog() []flashEntry {
	flashMu.Lock()
	defer flashMu.Unlock()
	return append([]flashEntry(nil), flashHistory...)
}

func (e flashEntry) levelName() string {
	switch e.level {
	case FlashError:
		return "ERROR"
	case FlashWarn:
		return "WARN"
	default:
		return "INFO"
	}
}

// showFlashLog renders the recent flash history as a scrollable view.
func (a *App) showFlashLog() {
	entries := flashLog()
	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("No messages yet.")
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		b.WriteString(fmt.Sprintf("%s  %-5s  %s\n", e.at.Format("15:04:05"), e.levelName(), e.text))
	}
	a.showDetailContent(views.DetailContent{Title: "flash history", Body: b.String()})
}
