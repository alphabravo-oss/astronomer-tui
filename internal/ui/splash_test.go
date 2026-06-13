package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func TestShowSplashRendersBanner(t *testing.T) {
	app := New(Options{
		Version: "v1.2.3",
		Client:  &cluster.Client{Context: "kind-kind", Namespace: "default"},
	})
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	app.app.SetScreen(screen)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.showSplash(ctx)
	done := make(chan error, 1)
	go func() { done <- app.app.Run() }()
	defer func() {
		app.app.Stop()
		if err := <-done; err != nil {
			t.Fatalf("tview run: %v", err)
		}
	}()

	if !waitForScreenText(t, app, screen, "Astronomer TUI") {
		t.Fatal("splash did not render the product name")
	}
	if !waitForScreenText(t, app, screen, "by AlphaBravo") {
		t.Fatal("splash did not render the AlphaBravo attribution")
	}
	if !waitForScreenText(t, app, screen, "v1.2.3") {
		t.Fatal("splash did not render the version")
	}
}

func waitForScreenText(t *testing.T, app *App, screen tcell.SimulationScreen, want string) bool {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(captureScreen(app, screen), want) {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// captureScreen reads the simulation buffer on tview's own goroutine so the read
// is serialized with the draw loop instead of racing it.
func captureScreen(app *App, screen tcell.SimulationScreen) string {
	result := make(chan string, 1)
	app.app.QueueUpdate(func() { result <- screenText(screen) })
	select {
	case s := <-result:
		return s
	case <-time.After(time.Second):
		return ""
	}
}

func screenText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var b strings.Builder
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			runes := cells[row*width+col].Runes
			if len(runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(runes[0])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
