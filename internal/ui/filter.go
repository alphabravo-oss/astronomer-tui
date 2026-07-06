//revive:disable:exported package-comments
package ui

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

type filterMode int

const (
	filterSubstr filterMode = iota
	filterRegex
	filterFuzzy
	filterLabel
)

// RowFilter is a compiled table filter. Supported syntaxes (after the leading
// "/"): plain substring, "re:PATTERN" regex, "-f TERM" fuzzy subsequence,
// "-l SELECTOR" label selector, and a leading "!" to invert any of the text
// modes.
type RowFilter struct {
	raw     string
	empty   bool
	inverse bool
	mode    filterMode
	needle  string
	re      *regexp.Regexp
	sel     labels.Selector
	err     error
}

// ParseRowFilter compiles a filter expression. A parse error is retained on the
// result (Err) and also makes Matches reject everything, so callers can decide
// whether to apply it.
func ParseRowFilter(expr string) RowFilter {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return RowFilter{raw: expr, empty: true}
	}
	f := RowFilter{raw: trimmed}
	switch {
	case strings.HasPrefix(trimmed, "-l "):
		f.mode = filterLabel
		sel, err := labels.Parse(strings.TrimSpace(trimmed[len("-l "):]))
		if err != nil {
			f.err = fmt.Errorf("invalid label selector: %w", err)
			return f
		}
		f.sel = sel
		return f
	case strings.HasPrefix(trimmed, "-f "):
		f.mode = filterFuzzy
		f.needle = strings.ToLower(strings.TrimSpace(trimmed[len("-f "):]))
		return f
	}
	// Optional inversion prefix applies to the text modes.
	body := trimmed
	if strings.HasPrefix(body, "!") {
		f.inverse = true
		body = strings.TrimSpace(body[1:])
	}
	if strings.HasPrefix(body, "re:") {
		f.mode = filterRegex
		re, err := regexp.Compile("(?i)" + strings.TrimSpace(body[len("re:"):]))
		if err != nil {
			f.err = fmt.Errorf("invalid regex: %w", err)
			return f
		}
		f.re = re
		return f
	}
	f.mode = filterSubstr
	f.needle = strings.ToLower(body)
	return f
}

// Empty reports whether the filter matches everything (no-op).
func (f RowFilter) Empty() bool { return f.empty }

// Err returns the compile error, if any.
func (f RowFilter) Err() error { return f.err }

// Matches reports whether a row (its cells and the object's labels) passes.
func (f RowFilter) Matches(row []views.Cell, objLabels map[string]string) bool {
	if f.empty {
		return true
	}
	if f.err != nil {
		return false
	}
	if f.mode == filterLabel {
		return f.sel.Matches(labels.Set(objLabels))
	}
	hay := rowHaystack(row)
	var ok bool
	switch f.mode {
	case filterRegex:
		ok = f.re.MatchString(hay)
	case filterFuzzy:
		ok = fuzzyMatch(f.needle, strings.ToLower(hay))
	default:
		ok = strings.Contains(strings.ToLower(hay), f.needle)
	}
	if f.inverse {
		return !ok
	}
	return ok
}

// Describe renders a short label for the title bar.
func (f RowFilter) Describe() string {
	if f.empty {
		return ""
	}
	prefix := ""
	if f.inverse {
		prefix = "!"
	}
	switch f.mode {
	case filterLabel:
		return "-l " + f.sel.String()
	case filterFuzzy:
		return "-f " + f.needle
	case filterRegex:
		return prefix + "re:" + strings.TrimPrefix(f.re.String(), "(?i)")
	default:
		return prefix + f.needle
	}
}

func rowHaystack(row []views.Cell) string {
	parts := make([]string, len(row))
	for i, c := range row {
		parts[i] = c.Text
	}
	return strings.Join(parts, " ")
}

// fuzzyMatch reports whether all runes of needle appear in order within hay.
func fuzzyMatch(needle, hay string) bool {
	if needle == "" {
		return true
	}
	i := 0
	nr := []rune(needle)
	for _, r := range hay {
		if r == nr[i] {
			i++
			if i == len(nr) {
				return true
			}
		}
	}
	return false
}
