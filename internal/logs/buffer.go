//revive:disable:exported package-comments
package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxBytes = 16 << 20
	MaxLineBytes    = 64 << 10
)

type Entry struct {
	Time  time.Time
	Pod   string
	Line  string
	Parse ParsedLine
	Color int
}

type Buffer struct {
	mu       sync.RWMutex
	limit    int
	maxBytes int
	bytes    int
	entries  []Entry
}

func NewBuffer(limit int) *Buffer {
	if limit <= 0 {
		limit = 10_000
	}
	return NewBufferWithByteLimit(limit, DefaultMaxBytes)
}

func NewBufferWithByteLimit(limit, maxBytes int) *Buffer {
	if limit <= 0 {
		limit = 10_000
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Buffer{limit: limit, maxBytes: maxBytes}
}

func (b *Buffer) Append(pod, line string, ts time.Time) {
	if len(line) > MaxLineBytes {
		line = line[:MaxLineBytes] + "…[truncated]"
	}
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
	b.bytes += entrySize(entry)
	for len(b.entries) > b.limit || b.bytes > b.maxBytes {
		b.bytes -= entrySize(b.entries[0])
		b.entries[0] = Entry{}
		b.entries = b.entries[1:]
	}
}

func entrySize(entry Entry) int {
	size := len(entry.Pod) + len(entry.Line) + len(entry.Parse.Raw)
	for key, value := range entry.Parse.Fields {
		size += len(key) + len(value)
	}
	return size
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
	return atomicPrivateWrite(path, []byte(strings.Join(lines, "\n")))
}

func atomicPrivateWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".astrotui-logs-*") // #nosec G304 -- directory is explicitly user-selected.
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
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
