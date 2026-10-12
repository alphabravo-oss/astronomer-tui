package ui

import "github.com/gdamore/tcell/v2"

// Strip colors at the terminal boundary, including third-party widget defaults.
// Keep emphasis and convert contrasting backgrounds to inverse video for focus.
type monochromeScreen struct {
	tcell.Screen
	background tcell.Color
}

func (s monochromeScreen) style(style tcell.Style) tcell.Style {
	_, bg, attrs := style.Decompose()
	if bg != tcell.ColorDefault && bg != tcell.ColorBlack && bg != s.background {
		attrs |= tcell.AttrReverse
	}
	return tcell.StyleDefault.Attributes(attrs)
}
func (s monochromeScreen) SetContent(x, y int, main rune, combining []rune, style tcell.Style) {
	s.Screen.SetContent(x, y, main, combining, s.style(style))
}
func (s monochromeScreen) SetStyle(style tcell.Style)        { s.Screen.SetStyle(s.style(style)) }
func (s monochromeScreen) Fill(main rune, style tcell.Style) { s.Screen.Fill(main, s.style(style)) }
