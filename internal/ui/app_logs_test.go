package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/logs"
)

func TestLogViewStateOptions(t *testing.T) {
	st := &logViewState{autoscroll: true, tail: 200, sinceSeconds: 0}
	opts := st.options()
	if opts.TailLines == nil || *opts.TailLines != 200 {
		t.Fatalf("expected tail 200, got %#v", opts.TailLines)
	}
	if opts.SinceSeconds != nil {
		t.Fatal("expected no since when 0")
	}
	if !opts.Follow {
		t.Fatal("expected follow")
	}

	st.previous = true
	st.timestamps = true
	st.sinceSeconds = 300
	opts = st.options()
	if !opts.Previous || !opts.Timestamps {
		t.Fatal("expected previous+timestamps")
	}
	if opts.SinceSeconds == nil || *opts.SinceSeconds != 300 {
		t.Fatalf("expected since 300, got %#v", opts.SinceSeconds)
	}
}

func TestLogViewTitleReflectsState(t *testing.T) {
	st := &logViewState{autoscroll: false, sinceSeconds: 60, previous: true, pretty: true}
	title := st.title()
	for _, want := range []string{"paused", "since=1m0s", "prev", "json"} {
		if !strings.Contains(title, want) {
			t.Fatalf("title %q missing %q", title, want)
		}
	}
}

func TestRenderLogViewPrettyParsesJSON(t *testing.T) {
	entries := []logs.Entry{
		{Pod: "api", Line: `{"level":"error","msg":"boom","code":500}`, Parse: logs.ParseLine(`{"level":"error","msg":"boom","code":500}`), Time: time.Time{}},
	}
	out := renderLogView(entries, true)
	if !strings.Contains(out, "boom") || !strings.Contains(strings.ToUpper(out), "ERROR") {
		t.Fatalf("pretty output missing level/msg: %q", out)
	}
	if !strings.Contains(out, "code=500") {
		t.Fatalf("pretty output missing extra field: %q", out)
	}
	// Raw mode keeps the JSON line intact.
	raw := renderLogView(entries, false)
	if !strings.Contains(raw, `"msg":"boom"`) {
		t.Fatalf("raw output should preserve json: %q", raw)
	}
}

func TestSeverityColor(t *testing.T) {
	cases := map[string]string{"error": "red", "warn": "yellow", "info": "green", "debug": "gray"}
	for level, want := range cases {
		if got := severityColor(level); got != want {
			t.Fatalf("severityColor(%q)=%q want %q", level, got, want)
		}
	}
}
