//revive:disable:exported package-comments
package logs

import (
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	PodUID    types.UID
	ID        uint64
	Namespace string
	Container string
	Time      time.Time
	Pod       string
	Line      string
	Parse     ParsedLine
	Color     int
}

type Buffer struct {
	mu       sync.RWMutex
	limit    int
	next     int
	sequence uint64
	entries  []Entry
}

func NewBuffer(limit int) *Buffer {
	if limit <= 0 {
		limit = 10_000
	}
	return &Buffer{limit: limit}
}

func (b *Buffer) Append(pod, line string, ts time.Time) {
	b.AppendSource(PodLogTarget{Pod: pod}, line, ts)
}

func (b *Buffer) AppendSource(target PodLogTarget, line string, ts time.Time) {
	pod := target.Pod
	if ts.IsZero() {
		ts = time.Now()
	}
	entry := Entry{
		Time:      ts,
		Namespace: target.Namespace, Container: target.Container, PodUID: target.PodUID,
		Pod:   pod,
		Line:  line,
		Parse: ParseLine(line),
		Color: StableColorIndex(pod),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	entry.ID = b.sequence
	if len(b.entries) < b.limit {
		b.entries = append(b.entries, entry)
	} else {
		b.entries[b.next] = entry
		b.next = (b.next + 1) % b.limit
	}
}

func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, 0, len(b.entries))
	out = append(out, b.entries[b.next:]...)
	out = append(out, b.entries[:b.next]...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Source() < out[j].Source()
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
		line = PrefixPod(entry.Source(), line)
	}
	if !entry.Time.IsZero() {
		return fmt.Sprintf("%s %s", entry.Time.Format(time.RFC3339), line)
	}
	return line
}

func (e Entry) Source() string {
	s := e.Pod
	if e.Namespace != "" {
		s = e.Namespace + "/" + s
	}
	if e.Container != "" {
		s += "/" + e.Container
	}
	return s
}
