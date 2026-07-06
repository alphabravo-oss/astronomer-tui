package ui

import (
	"fmt"
	"testing"
)

func TestRecordFlashHistoryBounded(t *testing.T) {
	flashMu.Lock()
	flashHistory = nil
	flashMu.Unlock()

	for i := 0; i < 60; i++ {
		recordFlash(FlashInfo, fmt.Sprintf("msg %d", i))
	}
	log := flashLog()
	if len(log) != 50 {
		t.Fatalf("expected history capped at 50, got %d", len(log))
	}
	// Oldest 10 dropped: first retained is msg 10.
	if log[0].text != "msg 10" {
		t.Fatalf("expected oldest retained to be 'msg 10', got %q", log[0].text)
	}
	if log[len(log)-1].text != "msg 59" {
		t.Fatalf("expected newest to be 'msg 59', got %q", log[len(log)-1].text)
	}
}

func TestFlashLevelName(t *testing.T) {
	cases := map[FlashLevel]string{FlashInfo: "INFO", FlashWarn: "WARN", FlashError: "ERROR"}
	for level, want := range cases {
		if got := (flashEntry{level: level}).levelName(); got != want {
			t.Fatalf("level %d: got %q want %q", level, got, want)
		}
	}
}

func TestParseFlashCommand(t *testing.T) {
	cmd, ok := parseCommand(":flash")
	if !ok || cmd.Kind != "flash" {
		t.Fatalf("unexpected flash command %#v %v", cmd, ok)
	}
}
