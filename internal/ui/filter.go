package ui

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

type resourceFilter func([]views.Cell, *unstructured.Unstructured) bool

func compileResourceFilter(query string) (resourceFilter, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return func([]views.Cell, *unstructured.Unstructured) bool { return true }, nil
	}
	if strings.HasPrefix(query, "-l ") {
		selector, err := labels.Parse(strings.TrimSpace(query[3:]))
		if err != nil {
			return nil, err
		}
		return func(_ []views.Cell, obj *unstructured.Unstructured) bool {
			return selector.Matches(labels.Set(obj.GetLabels()))
		}, nil
	}
	if strings.HasPrefix(query, "-f ") {
		needle := []rune(strings.ToLower(strings.TrimSpace(query[3:])))
		return func(row []views.Cell, _ *unstructured.Unstructured) bool {
			for _, cell := range row {
				at := 0
				for _, r := range strings.ToLower(cell.Text) {
					if at < len(needle) && needle[at] == r {
						at++
					}
				}
				if at == len(needle) {
					return true
				}
			}
			return false
		}, nil
	}
	if strings.HasPrefix(query, "-t ") {
		text := strings.ToLower(query[3:])
		return func(row []views.Cell, _ *unstructured.Unstructured) bool { return rowMatches(row, text) }, nil
	}
	inverse := strings.HasPrefix(query, "!")
	if inverse {
		query = strings.TrimSpace(query[1:])
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, fmt.Errorf("regex: %w", err)
	}
	literal := regexp.QuoteMeta(query) == query
	return func(row []views.Cell, _ *unstructured.Unstructured) bool {
		matched := false
		if literal {
			matched = rowMatches(row, strings.ToLower(query))
		} else {
			for _, cell := range row {
				if re.MatchString(cell.Text) {
					matched = true
					break
				}
			}
		}
		return matched != inverse
	}, nil
}

func compareCells(column string, a, b string, left, right *unstructured.Unstructured) int {
	if column == "AGE" {
		lt, rt := left.GetCreationTimestamp().Time, right.GetCreationTimestamp().Time
		// Age increases as the creation timestamp gets older.
		if !lt.IsZero() && !rt.IsZero() {
			return rt.Compare(lt)
		}
	}
	if strings.Contains(column, "CPU") || strings.Contains(column, "MEM") {
		av, ae := resource.ParseQuantity(strings.TrimSuffix(a, "%"))
		bv, be := resource.ParseQuantity(strings.TrimSuffix(b, "%"))
		if ae == nil && be == nil {
			return av.Cmp(bv)
		}
	}
	av, ae := strconv.ParseFloat(a, 64)
	bv, be := strconv.ParseFloat(b, 64)
	if ae == nil && be == nil {
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}

func compareColumnCells(col views.Column, a, b string, left, right *unstructured.Unstructured) int {
	if col.Type == "number" || col.Type == "integer" {
		av, ae := resource.ParseQuantity(strings.TrimSuffix(a, "%"))
		bv, be := resource.ParseQuantity(strings.TrimSuffix(b, "%"))
		if ae == nil && be == nil {
			return av.Cmp(bv)
		}
	}
	if col.Type == "date" {
		at, ae := time.Parse(time.RFC3339Nano, a)
		bt, be := time.Parse(time.RFC3339Nano, b)
		if ae == nil && be == nil {
			return at.Compare(bt)
		}
	}
	return compareCells(col.Name, a, b, left, right)
}
