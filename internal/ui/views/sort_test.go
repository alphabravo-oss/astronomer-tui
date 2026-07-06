package views

import "testing"

func TestCompareCellValues(t *testing.T) {
	cases := []struct {
		a, b   string
		expect int
	}{
		{"2", "10", -1},          // numeric, not lexicographic
		{"9m", "3h", -1},         // minutes before hours
		{"3h", "2d", -1},         // hours before days
		{"58d", "9m", 1},         // days after minutes
		{"1m30s", "2m", -1},      // Go durations
		{"1/3", "2/3", -1},       // ready fractions
		{"", "0", 1},             // empty sorts last
		{"api", "worker", -1},    // fallback lexical
		{"Running", "Failed", 1}, // fallback lexical
	}
	for _, tc := range cases {
		if got := CompareCellValues(tc.a, tc.b); got != tc.expect {
			t.Fatalf("CompareCellValues(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.expect)
		}
	}
}

func TestSortRowsTyped(t *testing.T) {
	rows := [][]Cell{{{Text: "10"}}, {{Text: "2"}}, {{Text: ""}}}
	SortRows(rows, 0, false)
	if rows[0][0].Text != "2" || rows[1][0].Text != "10" || rows[2][0].Text != "" {
		t.Fatalf("unexpected order %#v", rows)
	}
}
