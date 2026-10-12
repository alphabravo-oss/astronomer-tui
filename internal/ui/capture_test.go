package ui

import (
	"fmt"
	"html"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"github.com/gdamore/tcell/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Optional, reproducible export of a real tcell simulation frame, using fixtures.
func TestResourceScreenFixture(t *testing.T) {
	app := qualityApp(t)
	app.renderer = views.Builtins()[app.currentGVR]
	app.options.Client.Context = "staging-eu"
	app.options.Client.Namespace = "payments"
	app.options.Version = "dev"
	app.sortCol = 1
	for i, name := range []string{"api-6f8d7b-2kx9m", "api-6f8d7b-q8w2n", "worker-74bdc-p3z8v", "checkout-7ccf4-m9l2r", "ledger-0"} {
		obj := podObject(name, "payments")
		obj.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-time.Duration(i+1) * time.Hour)))
		obj.Object["spec"] = map[string]any{"nodeName": "worker-01", "containers": []any{map[string]any{"name": "app"}}}
		status := map[string]any{"ready": true, "restartCount": int64(i), "state": map[string]any{"running": map[string]any{}}}
		if i == 2 {
			status["ready"] = false
			status["state"] = map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}
		}
		obj.Object["status"].(map[string]any)["containerStatuses"] = []any{status}
		app.objects = append(app.objects, obj)
	}
	usage := map[string]cluster.Usage{}
	for i, obj := range app.objects {
		usage[obj.GetNamespace()+"/"+obj.GetName()] = cluster.Usage{CPUMilli: int64(25 + i*31), MemoryBytes: int64(48+i*16) * 1024 * 1024, Timestamp: time.Now()}
	}
	app.renderer = views.MetricsRenderer{Renderer: app.renderer, Usage: usage, Now: time.Now()}
	app.renderCurrent()
	width, height := 120, 24
	if size := os.Getenv("ASTROTUI_SCREENSHOT_SIZE"); size != "" {
		if _, err := fmt.Sscanf(size, "%dx%d", &width, &height); err != nil || width < 1 || height < 1 {
			t.Fatalf("invalid screenshot size %q", size)
		}
	}
	screen := startQualityScreen(t, app, width, height)
	if width >= 80 && !waitForScreenText(t, app, screen, "CrashLoopBackOff") {
		t.Fatal("failure reason is hidden behind pod phase")
	}
	destination := os.Getenv("ASTROTUI_SCREENSHOT")
	if destination == "" {
		return
	}
	exportScreenSVG(t, app, screen, destination)
}

func exportScreenSVG(t *testing.T, app *App, screen tcell.SimulationScreen, destination string) {
	t.Helper()
	var svg strings.Builder
	app.app.QueueUpdate(func() {
		cells, width, height := screen.GetContents()
		fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><title>Astrotui terminal simulation with fixture resources</title><rect width="100%%" height="100%%" fill="#000"/><g font-family="DejaVu Sans Mono, monospace" font-size="14">`, width*9+24, height*19+24, width*9+24, height*19+24)
		for row := 0; row < height; row++ {
			for start := 0; start < width; {
				fg, bg, _ := cells[row*width+start].Style.Decompose()
				end := start
				var run strings.Builder
				for end < width {
					cell := cells[row*width+end]
					nextFG, nextBG, _ := cell.Style.Decompose()
					if nextFG != fg || nextBG != bg {
						break
					}
					if len(cell.Runes) == 0 {
						run.WriteByte(' ')
					} else {
						run.WriteString(string(cell.Runes))
					}
					end++
				}
				if bg.Hex() >= 0 {
					fmt.Fprintf(&svg, `<rect x="%d" y="%d" width="%d" height="19" fill="#%06x"/>`, 12+start*9, 12+row*19, (end-start)*9, bg.Hex())
				}
				color := fg.Hex()
				if color < 0 {
					color = 0xffffff
				}
				if strings.TrimSpace(run.String()) != "" {
					fmt.Fprintf(&svg, `<text xml:space="preserve" x="%d" y="%d" fill="#%06x" textLength="%d" lengthAdjust="spacingAndGlyphs">%s</text>`, 12+start*9, 27+row*19, color, (end-start)*9, html.EscapeString(run.String()))
				}
				start = end
			}
		}
		svg.WriteString("</g></svg>\n")
	})
	if err := os.WriteFile(destination, []byte(svg.String()), 0644); err != nil {
		t.Fatal(err)
	}
}
