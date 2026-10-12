package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const astroLogo = `    _        _               _____ _   _ ___
   / \   ___| |_ _ __ ___    |_   _| | | |_ _|
  / _ \ / __| __| '__/ _ \_____| | | | | || |
 / ___ \\__ \ |_| | | (_) |____| | | |_| || |
/_/   \_\___/\__|_|  \___/     |_|  \___/|___|`

// Keep branding in the live dashboard; it never delays startup or consumes input.
// Short terminals retain their rows with a compact two-line ASCII wordmark.
func (a *App) initBranding() {
	a.header.SetWrap(false)
	a.header.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		lines := []string{"[ Astro-TUI ]"}
		brandWidth := 17
		align := tview.AlignCenter
		if width >= 80 && height >= 6 {
			lines = strings.Split(astroLogo, "\n")
			brandWidth = 46
			align = tview.AlignLeft
		}
		// On exceptionally narrow terminals give the entire width to context.
		if width < 36 {
			return x, y, width, height
		}
		brandX := x + width - brandWidth
		for row, line := range lines {
			tview.Print(screen, line, brandX, y+row, brandWidth, align, skinColor(a.skin.Accent, tcell.ColorAqua))
		}
		tview.Print(screen, "by AlphaBravo", brandX, y+len(lines), brandWidth, tview.AlignCenter, skinColor(a.skin.FG, tcell.ColorWhite))
		return x, y, width - brandWidth - 1, height
	})
	a.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, height := screen.Size()
		headerHeight := 3
		if width >= 80 && height >= 24 {
			headerHeight = 6
		}
		a.root.ResizeItem(a.header, headerHeight, 0)
		return false
	})
}
