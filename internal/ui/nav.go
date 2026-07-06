//revive:disable:exported package-comments
package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

// navPage is a full-screen view on the navigation stack.
type navPage struct {
	name  string
	prim  tview.Primitive
	focus tview.Primitive // what to focus when revealed (defaults to prim)
	crumb string          // breadcrumb label; empty hides the page from crumbs
	onPop func()          // cancel goroutines/streams owned by this page
}

// Nav is a two-level navigation container over tview.Pages:
//   - a stack of full-screen views (index 0 is the root, never popped), and
//   - a single modal slot rendered above the top view for transient prompts,
//     confirmations, and messages.
//
// This gives k9s-style Esc-back and drill-down while keeping the simple
// "replace the modal" semantics the app relied on before.
type Nav struct {
	app       *tview.Application
	pages     *tview.Pages
	views     []navPage
	seq       int
	modalOpen bool
}

const modalPageName = "modal"

func newNav(app *tview.Application, pages *tview.Pages) *Nav {
	return &Nav{app: app, pages: pages}
}

// SetRootView installs the base page (the resource table). Must be called once
// before any push.
func (n *Nav) SetRootView(prim tview.Primitive, crumb string) {
	page := navPage{name: "view-root", prim: prim, focus: prim, crumb: crumb}
	n.views = []navPage{page}
	n.pages.AddPage(page.name, prim, true, true)
	n.reconcile()
}

// PushView stacks a full-screen view. Esc/back pops it and runs onPop.
func (n *Nav) PushView(prim, focus tview.Primitive, crumb string, onPop func()) {
	n.seq++
	if focus == nil {
		focus = prim
	}
	page := navPage{name: fmt.Sprintf("view-%d", n.seq), prim: prim, focus: focus, crumb: crumb, onPop: onPop}
	n.views = append(n.views, page)
	n.pages.AddPage(page.name, prim, true, true)
	n.reconcile()
}

// PopView removes the top view (never the root) and runs its onPop. Returns
// false when already at the root.
func (n *Nav) PopView() bool {
	if len(n.views) <= 1 {
		return false
	}
	top := n.views[len(n.views)-1]
	n.views = n.views[:len(n.views)-1]
	n.pages.RemovePage(top.name)
	if top.onPop != nil {
		top.onPop()
	}
	n.reconcile()
	return true
}

// ResetToRoot pops every stacked view back to the root, running each onPop.
// Used on context switch so no orphaned streams survive.
func (n *Nav) ResetToRoot() {
	for len(n.views) > 1 {
		top := n.views[len(n.views)-1]
		n.views = n.views[:len(n.views)-1]
		n.pages.RemovePage(top.name)
		if top.onPop != nil {
			top.onPop()
		}
	}
	n.CloseModal()
	n.reconcile()
}

// ShowModal renders a transient overlay above the current view, replacing any
// existing modal. Non-fullscreen primitives (centered dialogs) show the view
// behind them.
func (n *Nav) ShowModal(prim tview.Primitive, fullscreen bool) {
	n.CloseModal()
	n.pages.AddPage(modalPageName, prim, fullscreen, true)
	n.pages.SendToFront(modalPageName)
	n.modalOpen = true
	n.app.SetFocus(prim)
}

// CloseModal clears the modal slot and restores focus to the top view.
func (n *Nav) CloseModal() {
	if !n.modalOpen {
		return
	}
	n.pages.RemovePage(modalPageName)
	n.modalOpen = false
	n.focusTop()
}

// ModalOpen reports whether a modal is currently displayed.
func (n *Nav) ModalOpen() bool { return n.modalOpen }

// TopView returns the primitive of the topmost stacked view.
func (n *Nav) TopView() tview.Primitive {
	if len(n.views) == 0 {
		return nil
	}
	return n.views[len(n.views)-1].prim
}

// Depth is the number of stacked views (1 = root only).
func (n *Nav) Depth() int { return len(n.views) }

// Crumbs returns the breadcrumb trail of the current view stack.
func (n *Nav) Crumbs() []string {
	out := make([]string, 0, len(n.views))
	for _, v := range n.views {
		if v.crumb != "" {
			out = append(out, v.crumb)
		}
	}
	return out
}

// SetTopCrumb updates the breadcrumb label of the current top view (used when a
// view's content changes, e.g. the resource table switching resources).
func (n *Nav) SetTopCrumb(crumb string) {
	if len(n.views) == 0 {
		return
	}
	n.views[len(n.views)-1].crumb = crumb
}

func (n *Nav) reconcile() {
	// Only the top view is visible; lower views are hidden to avoid draw cost.
	for i, v := range n.views {
		if i == len(n.views)-1 {
			n.pages.ShowPage(v.name)
		} else {
			n.pages.HidePage(v.name)
		}
	}
	if n.modalOpen {
		n.pages.SendToFront(modalPageName)
		return
	}
	n.focusTop()
}

func (n *Nav) focusTop() {
	if len(n.views) == 0 {
		return
	}
	top := n.views[len(n.views)-1]
	focus := top.focus
	if focus == nil {
		focus = top.prim
	}
	n.app.SetFocus(focus)
}

// --- App-side conveniences that wrap Nav and refresh the chrome ---

// showModal renders a transient overlay (input prompt, confirmation, message).
// It replaces any modal currently shown, matching the app's prior SetRoot
// semantics while keeping the underlying view visible.
func (a *App) showModal(prim tview.Primitive) {
	a.nav.ShowModal(prim, true)
}

// showModalFullscreen is showModal for overlays that should cover the screen
// (e.g. the splash banner).
func (a *App) showModalFullscreen(prim tview.Primitive) {
	a.nav.ShowModal(prim, true)
}

// closeModal dismisses the current overlay and returns focus to the view.
func (a *App) closeModal() {
	a.nav.CloseModal()
	a.updateChrome()
}

// pushView stacks a full-screen view; Esc/back pops it and runs onPop.
func (a *App) pushView(prim, focus tview.Primitive, crumb string, onPop func()) {
	a.nav.PushView(prim, focus, crumb, onPop)
	a.updateChrome()
}

// back pops the top view. Returns false at the root.
func (a *App) back() bool {
	popped := a.nav.PopView()
	a.updateChrome()
	return popped
}

// handleEsc is the single Esc policy: close a modal, else pop a view, else
// clear an active filter at the root.
func (a *App) handleEsc() {
	if a.nav.ModalOpen() {
		a.closeModal()
		return
	}
	if a.nav.Depth() > 1 {
		a.back()
		return
	}
	a.mu.RLock()
	filter := a.filter
	a.mu.RUnlock()
	if filter != "" {
		a.mu.Lock()
		a.filter = ""
		a.mu.Unlock()
		a.renderCurrent()
	}
}

// updateChrome redraws the header and breadcrumb bars.
func (a *App) updateChrome() {
	if a.crumbsView != nil {
		crumbs := a.nav.Crumbs()
		accent := tagColor(a.skin.Accent, "aqua")
		subtle := tagColor(a.skin.Subtle, "gray")
		parts := make([]string, len(crumbs))
		for i, c := range crumbs {
			if i == len(crumbs)-1 {
				parts[i] = fmt.Sprintf("[%s::b]%s[-:-:-]", accent, c)
			} else {
				parts[i] = fmt.Sprintf("[%s]%s[-:-:-]", subtle, c)
			}
		}
		a.crumbsView.SetText(" " + strings.Join(parts, fmt.Sprintf("[%s] › [-:-:-]", subtle)))
	}
	a.updateHeader()
}

// updateHeader renders the top chrome line: context/namespace, resource, and
// version. P2-5 expands this with cluster/user/gauges.
func (a *App) updateHeader() {
	if a.header == nil {
		return
	}
	accent := tagColor(a.skin.Accent, "aqua")
	subtle := tagColor(a.skin.Subtle, "gray")
	ctxName, ns := "", ""
	if a.options.Client != nil {
		ctxName = a.options.Client.Context
		ns = displayNamespace(a.options.Client.Namespace)
	}
	a.mu.RLock()
	resource := a.currentGVR.Resource
	a.mu.RUnlock()
	version := strings.TrimSpace(a.options.Version)
	left := fmt.Sprintf(" [%s::b]astrotui[-:-:-] [%s]%s[-:-:-]/[%s]%s[-:-:-]", accent, accent, ctxName, accent, ns)
	right := fmt.Sprintf("[%s]%s[-:-:-]", subtle, resource)
	if version != "" {
		right += fmt.Sprintf(" [%s]%s[-:-:-]", subtle, version)
	}
	a.header.SetText(left + "  " + right)
}
