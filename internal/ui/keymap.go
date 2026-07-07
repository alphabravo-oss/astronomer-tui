//revive:disable:exported package-comments
package ui

import (
	"fmt"
	"strings"

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

// parseHotkeySpec converts a hotkeys.yaml key string into a KeyBinding trigger
// (Key/Rune). Supports single runes, f1-f12, ctrl-<letter>, and shift-<digit>.
func parseHotkeySpec(spec string) (tcell.Key, rune, bool) {
	s := strings.ToLower(strings.TrimSpace(spec))
	if s == "" {
		return 0, 0, false
	}
	if len([]rune(s)) == 1 {
		return tcell.KeyRune, []rune(s)[0], true
	}
	if fkey, ok := fKeys[s]; ok {
		return fkey, 0, true
	}
	if rest, ok := strings.CutPrefix(s, "ctrl-"); ok && len(rest) == 1 {
		c := rest[0]
		if c >= 'a' && c <= 'z' {
			return tcell.Key(int(tcell.KeyCtrlA) + int(c-'a')), 0, true
		}
	}
	if rest, ok := strings.CutPrefix(s, "shift-"); ok && len(rest) == 1 {
		if sym, ok := shiftDigitSymbols[rest[0]]; ok {
			return tcell.KeyRune, sym, true
		}
		if rest[0] >= 'a' && rest[0] <= 'z' {
			return tcell.KeyRune, rune(rest[0] - 'a' + 'A'), true
		}
	}
	return 0, 0, false
}

var fKeys = map[string]tcell.Key{
	"f1": tcell.KeyF1, "f2": tcell.KeyF2, "f3": tcell.KeyF3, "f4": tcell.KeyF4,
	"f5": tcell.KeyF5, "f6": tcell.KeyF6, "f7": tcell.KeyF7, "f8": tcell.KeyF8,
	"f9": tcell.KeyF9, "f10": tcell.KeyF10, "f11": tcell.KeyF11, "f12": tcell.KeyF12,
}

var shiftDigitSymbols = map[byte]rune{
	'1': '!', '2': '@', '3': '#', '4': '$', '5': '%',
	'6': '^', '7': '&', '8': '*', '9': '(', '0': ')',
}

// rune builds a printable-rune binding.
func runeBinding(r rune, name, desc, scope string, visible bool, action func()) KeyBinding {
	return KeyBinding{Key: tcell.KeyRune, Rune: r, Name: name, Desc: desc, Scope: scope, Visible: visible, Action: action}
}

// keyBinding builds a special-key binding.
func keyBinding(key tcell.Key, name, desc, scope string, visible bool, action func()) KeyBinding {
	return KeyBinding{Key: key, Name: name, Desc: desc, Scope: scope, Visible: visible, Action: action}
}
