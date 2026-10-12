package ui

import (
	"fmt"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/rivo/tview"
)

func (a *App) showAdministrativeAction(name string) {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	req := actionRequest(name, a.currentGVR, obj)
	if safety.PolicyFor(a.options.Config, a.options.Client.Context) == "read-only" {
		a.showMessage("Action blocked", safety.ErrReadOnly.Error())
		return
	}
	text := fmt.Sprintf("%s %s/%s in context %s?", name, obj.GetKind(), obj.GetName(), a.options.Client.Context)
	if name == "drain" {
		text += "\nCordon node and evict managed pods. Mirror/DaemonSet pods remain. Unmanaged pods and emptyDir data block draining. Cancellation leaves the node cordoned; inspect it before uncordoning."
	}
	modal := tview.NewModal().SetText(tview.Escape(text)).AddButtons([]string{"Cancel", "Continue"}).SetDoneFunc(func(i int, _ string) {
		if i != 1 {
			a.showMain()
			return
		}
		if safety.RequiresConfirmation(a.options.Config, a.options.Client.Context, safety.Patch) {
			a.showTypedAction(req, safety.Patch, "Action completed", "Action failed")
		} else {
			a.runAction(req, "Action completed", "Action failed")
		}
	})
	a.app.SetRoot(modal, false)
}
