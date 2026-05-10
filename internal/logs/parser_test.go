package logs

import "testing"

func TestParseJSONAndFilter(t *testing.T) {
	lines := []ParsedLine{
		ParseLine(`{"level":"error","msg":"timeout"}`),
		ParseLine(`{"level":"info","msg":"ok"}`),
	}
	got := Filter(lines, `:level=error`)
	if len(got) != 1 || got[0].Fields["msg"] != "timeout" {
		t.Fatalf("unexpected filter result %#v", got)
	}
}

func TestParseLogfmt(t *testing.T) {
	line := ParseLine(`level=error msg=timeout`)
	if line.Format != "logfmt" || line.Fields["level"] != "error" {
		t.Fatalf("unexpected parse %#v", line)
	}
}

func TestStableColor(t *testing.T) {
	first := StableColorIndex("api-1")
	second := StableColorIndex("api-1")
	if first != second {
		t.Fatal("color index should be deterministic")
	}
}
