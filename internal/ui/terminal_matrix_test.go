package ui

import (
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/ui/theme"
	"github.com/gdamore/tcell/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func TestTerminalSizeUnicodeAndThemeMatrix(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}, {200, 60}} {
		for _, skin := range []string{"dark", "light", "dim"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], skin), func(t *testing.T) {
				app := qualityApp(t)
				app.skin = theme.Defaults()[skin]
				obj := podObject("api", "default")
				obj.SetLabels(map[string]string{"description": "日本語 🚀 café"})
				app.objects = []*unstructured.Unstructured{obj}
				app.renderCurrent()
				screen := startQualityScreen(t, app, size[0], size[1])
				app.app.QueueUpdateDraw(func() { screen.SetSize(size[0]+10, size[1]+3) })
				app.app.QueueEvent(tcell.NewEventResize(size[0]+10, size[1]+3))
				if !waitForScreenText(t, app, screen, "Astro-TUI") {
					t.Fatal("resize lost frame")
				}
			})
		}
	}
}

func TestNoColorRetainsFocusAndLiteralUnicode(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(40, 12)
	mono := monochromeScreen{Screen: screen, background: tcell.ColorBlack}
	mono.SetContent(1, 1, '界', nil, tcell.StyleDefault.Foreground(tcell.ColorRed).Background(tcell.ColorBlue).Bold(true))
	_, _, style, _ := screen.GetContent(1, 1)
	fg, bg, attrs := style.Decompose()
	if fg != tcell.ColorDefault || bg != tcell.ColorDefault {
		t.Fatal("color escaped terminal boundary")
	}
	if attrs&tcell.AttrReverse == 0 || attrs&tcell.AttrBold == 0 {
		t.Fatal("focus/emphasis lost")
	}
}
