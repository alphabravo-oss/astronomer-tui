package ui

import (
	"os"
	"testing"
)

func TestSafeRecoversAndWritesCrash(t *testing.T) {
	dir := t.TempDir()
	var report CrashReport
	panicked := SafeWithReport(CrashReporter{Dir: dir}, func() {
		panic("boom")
	}, func(got CrashReport) {
		report = got
	})
	if !panicked {
		t.Fatal("expected panic to be recovered")
	}
	if report.Path == "" || report.Err != nil {
		t.Fatalf("expected crash path without write error, got %#v", report)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one crash log, got %d", len(entries))
	}
}

func TestAppSafeShowsCrashMessageAndWritesLog(t *testing.T) {
	dir := t.TempDir()
	app := New(Options{})
	app.crashes = CrashReporter{Dir: dir}
	if !app.safe(func() { panic("boom") }) {
		t.Fatal("expected panic to be recovered")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one crash log, got %d", len(entries))
	}
}
