//revive:disable:exported package-comments
package logs

import (
	"encoding/json"
	"hash/fnv"
	"regexp"
	"strings"
)

type ParsedLine struct {
	Raw    string
	Fields map[string]string
	Format string
}

func ParseLine(line string) ParsedLine {
	out := ParsedLine{Raw: line, Fields: map[string]string{}, Format: "text"}
	var js map[string]any
	if json.Unmarshal([]byte(line), &js) == nil && len(js) > 0 {
		out.Format = "json"
		for k, v := range js {
			out.Fields[k] = strings.TrimSpace(strings.Trim(strings.ReplaceAll(strings.TrimSpace(toString(v)), "\n", " "), `"`))
		}
		return out
	}
	fields := parseLogfmt(line)
	if len(fields) > 0 {
		out.Format = "logfmt"
		out.Fields = fields
	}
	return out
}

func Filter(lines []ParsedLine, expr string) []ParsedLine {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return lines
	}
	if strings.Contains(expr, "~=") {
		parts := strings.SplitN(expr, "~=", 2)
		key := strings.TrimPrefix(strings.TrimSpace(parts[0]), ":")
		pattern := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil
		}
		return filter(lines, func(line ParsedLine) bool { return re.MatchString(line.Fields[key]) })
	}
	if strings.Contains(expr, "=") {
		parts := strings.SplitN(expr, "=", 2)
		key := strings.TrimPrefix(strings.TrimSpace(parts[0]), ":")
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		return filter(lines, func(line ParsedLine) bool { return line.Fields[key] == value })
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	return filter(lines, func(line ParsedLine) bool { return re.MatchString(line.Raw) })
}

func StableColorIndex(pod string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(pod))
	return int(h.Sum32() % 16)
}

func PrefixPod(pod, line string) string {
	return "[" + pod + "] " + line
}

func parseLogfmt(line string) map[string]string {
	fields := map[string]string{}
	for _, part := range strings.Fields(line) {
		key, value, ok := strings.Cut(part, "=")
		if !ok || key == "" {
			continue
		}
		fields[key] = strings.Trim(value, `"`)
	}
	return fields
}

func filter(lines []ParsedLine, keep func(ParsedLine) bool) []ParsedLine {
	out := make([]ParsedLine, 0, len(lines))
	for _, line := range lines {
		if keep(line) {
			out = append(out, line)
		}
	}
	return out
}

func toString(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	default:
		data, _ := json.Marshal(typed)
		return string(data)
	}
}
