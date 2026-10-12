package integrations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeToolContracts(t *testing.T) {
	if _, err := Tool(context.Background(), "astrotui-missing-tool-unique", nil, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing executable: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fixture")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\njson) printf '%s' '{\"matches\":[]}' ;;\nfail) echo fixture-error >&2; exit 7 ;;\nwait) exec sleep 30 ;;\nesac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := Tool(context.Background(), script, []string{"json"}, nil)
	if err != nil || string(data) != `{"matches":[]}` {
		t.Fatalf("output %q: %v", data, err)
	}
	if _, err = Tool(context.Background(), script, []string{"fail"}, nil); err == nil || !strings.Contains(err.Error(), "fixture-error") {
		t.Fatalf("stderr: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err = Tool(ctx, script, []string{"wait"}, nil); err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("cancellation: %v", err)
	}
	b := &limitedOutput{limit: 4}
	if n, err := b.Write([]byte("oversized")); n != 9 || err != nil || !b.overflow || b.data.String() != "over" {
		t.Fatal("output limit failed")
	}
}
