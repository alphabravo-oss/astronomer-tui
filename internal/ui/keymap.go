//revive:disable:exported package-comments
package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// KeyBinding is one entry in the keymap registry. Exactly one of Key/Rune
// identifies the trigger.
type KeyBinding struct {
	Key     tcell.Key // tcell.KeyRune when matching a printable rune
	Rune    rune
	Name    string
	Desc    string
	Scope   string
	Visible bool
	Action  func()
}

// Keymap is an ordered registry of key bindings. It replaces the hardcoded
// if-ladder so bindings can be introspected for the help overlay and, later,
// overridden by hotkeys.yaml.
type Keymap struct {
	bindings []KeyBinding
}

// Add registers a binding.
func (k *Keymap) Add(b KeyBinding) { k.bindings = append(k.bindings, b) }

// handle dispatches an event to the first matching binding, returning true if
// handled.
func (k *Keymap) handle(ev *tcell.EventKey) bool {
	for _, b := range k.bindings {
		if bindingMatches(b, ev) {
			if b.Action != nil {
				b.Action()
			}
			return true
		}
	}
	return false
}

func bindingMatches(b KeyBinding, ev *tcell.EventKey) bool {
	if b.Key == tcell.KeyRune {
		return ev.Key() == tcell.KeyRune && ev.Rune() == b.Rune
	}
	return ev.Key() == b.Key
}

// HelpRows returns visible bindings as (key, "name — desc") pairs, preserving
// registration order.
func (k *Keymap) HelpRows() [][2]string {
	var rows [][2]string
	for _, b := range k.bindings {
		if !b.Visible {
			continue
		}
		rows = append(rows, [2]string{describeKey(b), fmt.Sprintf("%s — %s", b.Name, b.Desc)})
	}
	return rows
}

// describeKey renders a human-readable label for a binding's trigger.
func describeKey(b KeyBinding) string {
	if b.Key == tcell.KeyRune {
		return string(b.Rune)
	}
	if name, ok := keyNames[b.Key]; ok {
		return name
	}
	return tcell.KeyNames[b.Key]
}

var keyNames = map[tcell.Key]string{
	tcell.KeyEnter:  "Enter",
	tcell.KeyEscape: "Esc",
	tcell.KeyCtrlP:  "Ctrl+P",
	tcell.KeyCtrlD:  "Ctrl+D",
	tcell.KeyCtrlE:  "Ctrl+E",
	tcell.KeyCtrlK:  "Ctrl+K",
	tcell.KeyCtrlR:  "Ctrl+R",
	tcell.KeyCtrlW:  "Ctrl+W",
	tcell.KeyCtrlL:  "Ctrl+L",
	tcell.KeyCtrlS:  "Ctrl+S",
}

// rune builds a printable-rune binding.
func runeBinding(r rune, name, desc, scope string, visible bool, action func()) KeyBinding {
	return KeyBinding{Key: tcell.KeyRune, Rune: r, Name: name, Desc: desc, Scope: scope, Visible: visible, Action: action}
}

// keyBinding builds a special-key binding.
func keyBinding(key tcell.Key, name, desc, scope string, visible bool, action func()) KeyBinding {
	return KeyBinding{Key: key, Name: name, Desc: desc, Scope: scope, Visible: visible, Action: action}
}
