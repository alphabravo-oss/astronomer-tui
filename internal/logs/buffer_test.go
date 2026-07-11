package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBufferBoundsAndSortsMergedEntries(t *testing.T) {
	buffer := NewBuffer(2)
	now := time.Now()
	buffer.Append("api-b", "level=info msg=second", now.Add(time.Second))
	buffer.Append("api-a", "level=error msg=first", now)
	buffer.Append("api-c", "level=error msg=third", now.Add(2*time.Second))
	entries := buffer.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected bounded buffer, got %d", len(entries))
	}
	if entries[0].Pod != "api-a" || entries[1].Pod != "api-c" {
		t.Fatalf("unexpected order %#v", entries)
	}
}

func TestBufferFilterRegexAndSave(t *testing.T) {
	buffer := NewBuffer(10)
	buffer.Append("api", `{"level":"error","msg":"timeout"}`, time.Unix(1, 0))
	buffer.Append("api", `{"level":"info","msg":"ok"}`, time.Unix(2, 0))
	filtered := buffer.Filter(":level=error")
	if len(filtered) != 1 || filtered[0].Parse.Fields["msg"] != "timeout" {
		t.Fatalf("unexpected filter %#v", filtered)
	}
	matches, err := buffer.RegexMatches("time")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != 0 {
		t.Fatalf("unexpected matches %#v", matches)
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	if err := buffer.Save(path, ":level=error"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test reads a file created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[api]") || strings.Contains(string(data), "ok") {
		t.Fatalf("unexpected save output %s", data)
	}
}

func TestBufferAssignsStablePodColor(t *testing.T) {
	buffer := NewBuffer(10)
	buffer.Append("api-1", "hello", time.Unix(1, 0))
	entries := buffer.Entries()
	if len(entries) != 1 || entries[0].Color != StableColorIndex("api-1") {
		t.Fatalf("unexpected entries %#v", entries)
	}
}

func TestBufferBoundsBytesAndSaveRepairsPermissions(t *testing.T) {
	buffer := NewBufferWithByteLimit(100, 256)
	for range 20 {
		buffer.Append("api", strings.Repeat("x", 40), time.Now())
	}
	if len(buffer.Entries()) >= 20 {
		t.Fatal("expected byte budget to evict entries")
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Save(path, ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected private export mode, got %o", info.Mode().Perm())
	}
}
