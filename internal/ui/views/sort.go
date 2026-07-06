//revive:disable:exported package-comments
package views

import (
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// CompareCellValues orders two rendered cell values the way a human expects:
// numerically for numbers ("2" < "10"), by magnitude for ages and durations
// ("9m" < "3h" < "2d"), by parts for ready fractions ("1/3" < "2/3"), and
// lexically otherwise. Empty values always sort last.
func CompareCellValues(a, b string) int {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	if av, aok := parseNumeric(a); aok {
		if bv, bok := parseNumeric(b); bok {
			return compareFloats(av, bv)
		}
	}
	if av, aok := parseCellDuration(a); aok {
		if bv, bok := parseCellDuration(b); bok {
			return compareFloats(float64(av), float64(bv))
		}
	}
	if an, ad, aok := parseFraction(a); aok {
		if bn, bd, bok := parseFraction(b); bok {
			if c := compareFloats(an, bn); c != 0 {
				return c
			}
			return compareFloats(ad, bd)
		}
	}
	return strings.Compare(a, b)
}

// CompareCellValuesFor is column-aware: CPU/MEM columns are compared as
// Kubernetes quantities (so "142m" < "1" core and "512Mi" < "1.2Gi"), percent
// columns numerically, and everything else falls back to CompareCellValues.
// This disambiguates "142m" (millicores) from "10m" (age minutes), which live
// in different columns.
func CompareCellValuesFor(column, a, b string) int {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	up := strings.ToUpper(column)
	if strings.HasPrefix(up, "%") {
		return comparePercent(a, b)
	}
	if strings.Contains(up, "CPU") || strings.Contains(up, "MEM") {
		if c, ok := compareQuantities(a, b); ok {
			return c
		}
	}
	return CompareCellValues(a, b)
}

func comparePercent(a, b string) int {
	av, aok := parsePercent(a)
	bv, bok := parsePercent(b)
	if aok && bok {
		return compareFloats(av, bv)
	}
	if aok != bok { // real percent sorts before n/a
		if aok {
			return -1
		}
		return 1
	}
	return CompareCellValues(a, b)
}

func parsePercent(s string) (float64, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func compareQuantities(a, b string) (int, bool) {
	aq, aerr := resource.ParseQuantity(a)
	bq, berr := resource.ParseQuantity(b)
	if aerr == nil && berr == nil {
		return aq.Cmp(bq), true
	}
	if aerr == nil { // valid quantity sorts before n/a/empty
		return -1, true
	}
	if berr == nil {
		return 1, true
	}
	return 0, false
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func parseNumeric(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func parseFraction(s string) (float64, float64, bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, 0, false
	}
	den, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, 0, false
	}
	return num, den, true
}

// parseCellDuration understands the age formats this app renders ("12m",
// "3h", "58d") plus Go duration strings emitted for job durations ("1m30s").
func parseCellDuration(s string) (time.Duration, bool) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if v, err := strconv.ParseFloat(days, 64); err == nil {
			return time.Duration(v * 24 * float64(time.Hour)), true
		}
	}
	if v, err := time.ParseDuration(s); err == nil {
		return v, true
	}
	return 0, false
}
