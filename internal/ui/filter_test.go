package ui

import (
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

func cells(texts ...string) []views.Cell {
	out := make([]views.Cell, len(texts))
	for i, t := range texts {
		out[i] = views.Cell{Text: t}
	}
	return out
}

func TestRowFilterSubstringAndInverse(t *testing.T) {
	row := cells("api-7f9c", "default", "Running")
	if !ParseRowFilter("api").Matches(row, nil) {
		t.Fatal("expected substring match")
	}
	if ParseRowFilter("nginx").Matches(row, nil) {
		t.Fatal("expected substring miss")
	}
	if ParseRowFilter("!nginx").Matches(row, nil) != true {
		t.Fatal("expected inverse match for absent term")
	}
	if ParseRowFilter("!api").Matches(row, nil) != false {
		t.Fatal("expected inverse miss for present term")
	}
}

func TestRowFilterRegex(t *testing.T) {
	row := cells("api-7f9c", "Running")
	f := ParseRowFilter("re:api-[0-9a-f]+")
	if f.Err() != nil || !f.Matches(row, nil) {
		t.Fatalf("expected regex match, err=%v", f.Err())
	}
	bad := ParseRowFilter("re:[")
	if bad.Err() == nil {
		t.Fatal("expected regex compile error")
	}
	if bad.Matches(row, nil) {
		t.Fatal("errored filter must match nothing")
	}
}

func TestRowFilterFuzzy(t *testing.T) {
	row := cells("api-server-7f9c")
	if !ParseRowFilter("-f apsrv").Matches(row, nil) {
		t.Fatal("expected fuzzy subsequence match")
	}
	if ParseRowFilter("-f zzz").Matches(row, nil) {
		t.Fatal("expected fuzzy miss")
	}
}

func TestRowFilterLabelSelector(t *testing.T) {
	row := cells("api")
	f := ParseRowFilter("-l app=api,tier=web")
	if f.Err() != nil {
		t.Fatalf("unexpected selector error: %v", f.Err())
	}
	if !f.Matches(row, map[string]string{"app": "api", "tier": "web"}) {
		t.Fatal("expected label match")
	}
	if f.Matches(row, map[string]string{"app": "api"}) {
		t.Fatal("expected label miss when tier absent")
	}
	if ParseRowFilter("-l ==bad").Err() == nil {
		t.Fatal("expected invalid selector error")
	}
}

func TestRowFilterDescribe(t *testing.T) {
	if got := ParseRowFilter("!re:foo").Describe(); got != "!re:foo" {
		t.Fatalf("unexpected describe %q", got)
	}
	if got := ParseRowFilter("").Describe(); got != "" {
		t.Fatalf("expected empty describe, got %q", got)
	}
}
