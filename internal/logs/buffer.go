//revive:disable:exported package-comments
package logs

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time  time.Time
	Pod   string
	Line  string
	Parse ParsedLine
	Color int
}

type Buffer struct {
	mu      sync.RWMutex
	limit   int
	entries []Entry
}

func NewBuffer(limit int) *Buffer {
	if limit <= 0 {
		limit = 10_000
	}
	return &Buffer{limit: limit}
}

func (b *Buffer) Append(pod, line string, ts time.Time) {
	if ts.IsZero() {
		ts = time.Now()
	}
	entry := Entry{
		Time:  ts,
		Pod:   pod,
		Line:  line,
		Parse: ParseLine(line),
		Color: StableColorIndex(pod),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, entry)
	if overflow := len(b.entries) - b.limit; overflow > 0 {
		copy(b.entries, b.entries[overflow:])
		b.entries = b.entries[:b.limit]
	}
}

// Reset clears all buffered entries.
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = nil
}

func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := append([]Entry(nil), b.entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Pod < out[j].Pod
		}
		return out[i].Time.Before(out[j].Time)
	})
	return out
}

func (b *Buffer) Filter(expr string) []Entry {
	entries := b.Entries()
	parsed := make([]ParsedLine, len(entries))
	for i, entry := range entries {
		parsed[i] = entry.Parse
	}
	filtered := Filter(parsed, expr)
	if len(filtered) == len(parsed) && expr == "" {
		return entries
	}
	keep := map[string]int{}
	for _, line := range filtered {
		keep[line.Raw]++
	}
	out := make([]Entry, 0, len(filtered))
	for _, entry := range entries {
		if keep[entry.Parse.Raw] > 0 {
			out = append(out, entry)
			keep[entry.Parse.Raw]--
		}
	}
	return out
}

func (b *Buffer) RegexMatches(pattern string) ([]int, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	entries := b.Entries()
	var matches []int
	for i, entry := range entries {
		if re.MatchString(entry.Line) {
			matches = append(matches, i)
		}
	}
	return matches, nil
}

func (b *Buffer) Save(path, filterExpr string) error {
	entries := b.Filter(filterExpr)
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, FormatEntry(entry, true))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600) // #nosec G304 -- save path is explicitly user-selected.
}

func FormatEntry(entry Entry, prefixPod bool) string {
	line := entry.Line
	if prefixPod && entry.Pod != "" {
		line = PrefixPod(entry.Pod, line)
	}
	if !entry.Time.IsZero() {
		return fmt.Sprintf("%s %s", entry.Time.Format(time.RFC3339), line)
	}
	return line
}
