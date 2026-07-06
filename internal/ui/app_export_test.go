package ui

import (
	"strings"
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

func TestRowsToCSV(t *testing.T) {
	header := []views.Column{{Name: "NAME"}, {Name: "STATUS"}}
	rows := [][]views.Cell{
		{{Text: "api"}, {Text: "Running"}},
		{{Text: "db, primary"}, {Text: "Pending"}}, // comma forces quoting
	}
	out := rowsToCSV(header, rows)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != "NAME,STATUS" {
		t.Fatalf("unexpected header line %q", lines[0])
	}
	if lines[1] != "api,Running" {
		t.Fatalf("unexpected first row %q", lines[1])
	}
	if !strings.Contains(lines[2], `"db, primary"`) {
		t.Fatalf("expected quoted field, got %q", lines[2])
	}
}

func TestParseAliasCommand(t *testing.T) {
	cmd, ok := parseCommand(":alias")
	if !ok || cmd.Kind != "aliases-view" {
		t.Fatalf("unexpected alias command %#v %v", cmd, ok)
	}
}
