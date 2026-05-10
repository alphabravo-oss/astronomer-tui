package palette

import (
	"fmt"
	"testing"
)

func TestSearchFuzzy(t *testing.T) {
	idx := NewIndex([]Entry{
		{ID: "pod/default/api", Title: "pod/default/api", Category: Resources},
		{ID: "restart", Title: "restart deployment", Category: Actions},
	})
	matches := idx.Search("pda", 10)
	if len(matches) == 0 || matches[0].Entry.ID != "pod/default/api" {
		t.Fatalf("unexpected matches %#v", matches)
	}
}

func BenchmarkSearch10k(b *testing.B) {
	entries := make([]Entry, 10_000)
	for i := range entries {
		entries[i] = Entry{ID: fmt.Sprintf("pod/default/api-%d", i), Title: fmt.Sprintf("pod/default/api-%d", i), Category: Resources}
	}
	idx := NewIndex(entries)
	b.ResetTimer()
	for range b.N {
		_ = idx.Search("api-9999", 20)
	}
}
