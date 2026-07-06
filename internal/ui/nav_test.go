package ui

import (
	"testing"

	"github.com/rivo/tview"
)

func newTestNav() *Nav {
	app := tview.NewApplication()
	return newNav(app, tview.NewPages())
}

func TestNavPushPopStack(t *testing.T) {
	n := newTestNav()
	root := tview.NewBox()
	n.SetRootView(root, "pods")
	if n.Depth() != 1 {
		t.Fatalf("expected depth 1, got %d", n.Depth())
	}
	if n.PopView() {
		t.Fatal("popping the root should return false")
	}

	popped := false
	n.PushView(tview.NewBox(), nil, "api-7f9c", func() { popped = true })
	if n.Depth() != 2 {
		t.Fatalf("expected depth 2, got %d", n.Depth())
	}
	if got := n.Crumbs(); len(got) != 2 || got[0] != "pods" || got[1] != "api-7f9c" {
		t.Fatalf("unexpected crumbs %#v", got)
	}
	if !n.PopView() {
		t.Fatal("expected pop to succeed")
	}
	if !popped {
		t.Fatal("expected onPop to run")
	}
	if n.Depth() != 1 {
		t.Fatalf("expected depth back to 1, got %d", n.Depth())
	}
}

func TestNavModalSlot(t *testing.T) {
	n := newTestNav()
	n.SetRootView(tview.NewBox(), "pods")
	if n.ModalOpen() {
		t.Fatal("no modal expected initially")
	}
	n.ShowModal(tview.NewInputField(), true)
	if !n.ModalOpen() {
		t.Fatal("expected modal open")
	}
	// Showing another modal replaces the first without stacking.
	n.ShowModal(tview.NewInputField(), true)
	if !n.ModalOpen() {
		t.Fatal("expected modal still open after replace")
	}
	n.CloseModal()
	if n.ModalOpen() {
		t.Fatal("expected modal closed")
	}
}

func TestNavResetToRootRunsOnPop(t *testing.T) {
	n := newTestNav()
	n.SetRootView(tview.NewBox(), "pods")
	pops := 0
	n.PushView(tview.NewBox(), nil, "a", func() { pops++ })
	n.PushView(tview.NewBox(), nil, "b", func() { pops++ })
	n.ShowModal(tview.NewBox(), true)
	n.ResetToRoot()
	if n.Depth() != 1 {
		t.Fatalf("expected depth 1 after reset, got %d", n.Depth())
	}
	if pops != 2 {
		t.Fatalf("expected 2 onPop calls, got %d", pops)
	}
	if n.ModalOpen() {
		t.Fatal("expected modal closed after reset")
	}
}

func TestHandleEscPolicy(t *testing.T) {
	app := New(Options{})
	app.nav.SetRootView(app.table, "")

	// Modal open -> Esc closes modal.
	app.showModal(tview.NewInputField())
	app.handleEsc()
	if app.nav.ModalOpen() {
		t.Fatal("expected Esc to close modal")
	}

	// View pushed -> Esc pops it.
	app.pushView(tview.NewBox(), nil, "detail", nil)
	if app.nav.Depth() != 2 {
		t.Fatalf("expected depth 2, got %d", app.nav.Depth())
	}
	app.handleEsc()
	if app.nav.Depth() != 1 {
		t.Fatalf("expected Esc to pop view, depth %d", app.nav.Depth())
	}

	// At root with a filter -> Esc clears the filter.
	app.mu.Lock()
	app.filter = "foo"
	app.mu.Unlock()
	app.handleEsc()
	app.mu.RLock()
	f := app.filter
	app.mu.RUnlock()
	if f != "" {
		t.Fatalf("expected Esc to clear filter, got %q", f)
	}
}
