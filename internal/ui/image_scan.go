package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/integrations"
	"github.com/rivo/tview"
)

// Grype is an optional native integration, not a plugin. Scans are always explicit.
func (a *App) showImageScan() {
	images := objectImages(a.selectedObject())
	if len(images) == 0 {
		a.showMessage("Image scan", "Select a pod or workload with container images")
		return
	}
	a.showPicker("Select image to scan (requires grype)", images, func(image string) {
		modal := tview.NewModal().SetText(tview.Escape("Scan " + image + "?\nGrype may download the image and vulnerability database using local registry credentials. No Kubernetes resources are changed.")).AddButtons([]string{"Cancel", "Scan"}).SetDoneFunc(func(i int, _ string) {
			if i != 1 {
				a.showMain()
				return
			}
			a.operationWithTimeout("Scan image", 5*time.Minute, func(ctx context.Context) (func(), error) {
				data, err := integrations.Tool(ctx, "grype", []string{image, "--output", "json"}, nil)
				if err != nil {
					return nil, err
				}
				var report struct {
					Matches []struct {
						Vulnerability struct {
							ID, Severity, Description string
							Fix                       struct{ Versions []string }
						}
						Artifact struct{ Name, Version, Type string }
					}
				}
				if err = json.Unmarshal(data, &report); err != nil {
					return nil, err
				}
				rank := map[string]int{"Critical": 0, "High": 1, "Medium": 2, "Low": 3, "Negligible": 4, "Unknown": 5}
				sort.SliceStable(report.Matches, func(i, j int) bool {
					return rank[report.Matches[i].Vulnerability.Severity] < rank[report.Matches[j].Vulnerability.Severity]
				})
				return func() {
					table := a.explorerTable("Image vulnerabilities · "+image+" · Enter details · Esc back", []string{"SEVERITY", "VULNERABILITY", "PACKAGE", "VERSION", "FIXED IN"})
					for i, m := range report.Matches {
						a.setExplorerRow(table, i+1, m.Vulnerability.Severity, m.Vulnerability.ID, m.Artifact.Name, m.Artifact.Version, fmt.Sprint(m.Vulnerability.Fix.Versions))
					}
					if len(report.Matches) == 0 {
						a.setExplorerRow(table, 1, "No findings reported by this scan")
					}
					table.SetSelectedFunc(func(row, col int) {
						if row > 0 && row <= len(report.Matches) {
							body, _ := json.MarshalIndent(report.Matches[row-1], "", "  ")
							a.showDocument("Vulnerability details", string(body))
						}
					})
					table.SetInputCapture(a.backCapture())
					a.app.SetRoot(table, true).SetFocus(table)
				}, nil
			})
		})
		a.app.SetRoot(modal, false)
	})
}
