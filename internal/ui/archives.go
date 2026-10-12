package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/benchmark"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func (a *App) showArchives(kind string) {
	dir := filepath.Join(a.customDir(), kind)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		a.showMessage("Saved files", err.Error())
		return
	}
	list := tview.NewList().ShowSecondaryText(false)
	list.SetTitle(" " + kind + " · Enter open · Ctrl-D delete · Esc back ").SetBorder(true)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "benchmark-") && !strings.HasPrefix(name, "resource-") {
			continue
		}
		names = append(names, name)
		list.AddItem(tview.Escape(name), "", 0, func() {
			a.operation("Read saved file", func(ctx context.Context) (func(), error) {
				p := filepath.Join(dir, name)
				info, err := os.Lstat(p)
				if err != nil {
					return nil, err
				}
				if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
					return nil, fmt.Errorf("saved file is not regular or exceeds 16 MiB")
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return nil, err
				}
				body := string(data)
				if kind == "benchmarks" {
					var report benchmark.Report
					if err := json.Unmarshal(data, &report); err == nil {
						body = benchmark.Summary(report)
					}
				}
				return func() { a.showDocument(name, body) }, nil
			})
		})
	}
	if list.GetItemCount() == 0 {
		list.AddItem("No saved files", "", 0, nil)
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		if e.Key() == tcell.KeyCtrlD {
			row := list.GetCurrentItem()
			if row < 0 || row >= len(names) {
				return nil
			}
			name := names[row]
			modal := tview.NewModal().SetText("Delete saved file " + tview.Escape(name) + "?").AddButtons([]string{"Cancel", "Delete"}).SetDoneFunc(func(i int, _ string) {
				if i != 1 {
					a.showArchives(kind)
					return
				}
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					a.showMessage("Delete saved file", err.Error())
					return
				}
				a.showArchives(kind)
			})
			a.app.SetRoot(modal, false)
			return nil
		}
		return e
	})
	a.app.SetRoot(list, true).SetFocus(list)
}
func (a *App) saveSelectedResource() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	dir := filepath.Join(a.customDir(), "screendumps")
	save := func() {
		a.operation("Save resource", func(ctx context.Context) (func(), error) {
			data, err := yaml.Marshal(obj.Object)
			if err != nil {
				return nil, err
			}
			if err = os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
			f, err := os.CreateTemp(dir, ".resource-*.tmp")
			if err != nil {
				return nil, err
			}
			defer os.Remove(f.Name())
			_, err = f.Write(data)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return nil, err
			}
			p := filepath.Join(dir, "resource-"+time.Now().UTC().Format("20060102T150405.000000000")+".yaml")
			if err = os.Rename(f.Name(), p); err != nil {
				return nil, err
			}
			return func() { a.showMessage("Resource saved", p+"\n:sd opens saved resources") }, nil
		})
	}
	if obj.GetKind() == "Secret" {
		modal := tview.NewModal().SetText("Export this Secret to a local private file? The file contains recoverable credentials.").AddButtons([]string{"Cancel", "Export"}).SetDoneFunc(func(i int, _ string) {
			if i == 1 {
				save()
			} else {
				a.showMain()
			}
		})
		a.app.SetRoot(modal, false)
	} else {
		save()
	}
}
func (a *App) showDecodedSecret() {
	obj := a.selectedObject()
	if obj == nil || obj.GetKind() != "Secret" {
		a.showMessage("Decode Secret", "Select a Secret first")
		return
	}
	obj = obj.DeepCopy()
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	decoded := map[string]string{}
	for key, value := range data {
		b, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			decoded[key] = "<invalid base64>"
		} else {
			decoded[key] = string(b)
		}
	}
	body, _ := json.MarshalIndent(decoded, "", "  ")
	a.showDocument("Decoded Secret · "+obj.GetNamespace()+"/"+obj.GetName(), string(body))
}
