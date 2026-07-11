//revive:disable:exported package-comments
package palette

import (
	"sort"
	"strings"
)

type Category string

const (
	Resources  Category = "Resources"
	Actions    Category = "Actions"
	Contexts   Category = "Contexts"
	Namespaces Category = "Namespaces"
	Plugins    Category = "Plugins"
	AI         Category = "AI"
)

type Entry struct {
	ID          string
	Title       string
	Description string
	Category    Category
	Payload     any
}

type Match struct {
	Entry Entry
	Score int
}

type Index struct {
	entries []Entry
}

func NewIndex(entries []Entry) *Index {
	return &Index{entries: append([]Entry(nil), entries...)}
}

func (i *Index) Replace(entries []Entry) {
	i.entries = append(i.entries[:0], entries...)
}

func (i *Index) Search(query string, limit int) []Match {
	if limit <= 0 {
		limit = 50
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		out := make([]Match, 0, min(limit, len(i.entries)))
		for _, entry := range i.entries {
			out = append(out, Match{Entry: entry})
			if len(out) == limit {
				break
			}
		}
		return out
	}
	matches := make([]Match, 0, min(limit, len(i.entries)))
	for _, entry := range i.entries {
		haystack := strings.ToLower(entry.Title + " " + entry.Description + " " + string(entry.Category))
		score, ok := fuzzyScore(query, haystack)
		if ok {
			candidate := Match{Entry: entry, Score: score}
			if len(matches) < limit {
				matches = append(matches, candidate)
				continue
			}
			worst := 0
			for idx := 1; idx < len(matches); idx++ {
				if better(matches[worst], matches[idx]) {
					worst = idx
				}
			}
			if better(candidate, matches[worst]) {
				matches[worst] = candidate
			}
		}
	}
	sort.SliceStable(matches, func(a, b int) bool {
		return better(matches[a], matches[b])
	})
	return matches
}

func better(a, b Match) bool {
	if a.Score == b.Score {
		return a.Entry.Title < b.Entry.Title
	}
	return a.Score > b.Score
}

func fuzzyScore(needle, haystack string) (int, bool) {
	pos := -1
	score := 0
	streak := 0
	for _, ch := range needle {
		next := strings.IndexRune(haystack[pos+1:], ch)
		if next < 0 {
			return 0, false
		}
		abs := pos + 1 + next
		if abs == pos+1 {
			streak++
			score += 10 + streak*3
		} else {
			streak = 0
			score += 3
		}
		if abs == 0 || haystack[abs-1] == '/' || haystack[abs-1] == '-' || haystack[abs-1] == ' ' {
			score += 4
		}
		pos = abs
	}
	return score - pos/20, true
}
