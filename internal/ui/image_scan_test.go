package ui

import (
	"github.com/gdamore/tcell/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"os"
	"path/filepath"
	"testing"
)

func TestImageScanRequiresConfirmationAndRendersReport(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	t.Setenv("ASTROTUI_SCAN_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
if [ "$1" != "busybox:1.37.0" ] || [ "$2" != "--output" ] || [ "$3" != "json" ]; then exit 2; fi
touch "$ASTROTUI_SCAN_MARKER"
printf '%s' '{"matches":[{"vulnerability":{"id":"CVE-fixture","severity":"High","description":"fixture only","fix":{"versions":["2.0"]}},"artifact":{"name":"test-package","version":"1.0","type":"apk"}}]}'
`
	if err := os.WriteFile(filepath.Join(dir, "grype"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	app := qualityApp(t)
	obj := podObject("api", "default")
	obj.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "app", "image": "busybox:1.37.0"}}}
	app.objects = []*unstructured.Unstructured{obj}
	app.renderCurrent()
	screen := startQualityScreen(t, app, 120, 24)
	app.app.QueueUpdateDraw(app.showImageScan)
	if !waitForScreenText(t, app, screen, "Select image") {
		t.Fatal("picker missing")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !waitForScreenText(t, app, screen, "vulnerability database") {
		t.Fatal("confirmation missing")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("scanner ran before confirmation")
	}
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	app.app.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !waitForScreenText(t, app, screen, "test-package") || !waitForScreenText(t, app, screen, "CVE-fixture") {
		t.Fatalf("report missing: %s", captureScreen(app, screen))
	}
}
