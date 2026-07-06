//revive:disable:exported package-comments
package ui

import (
	"strings"
)

type Command struct {
	Kind  string
	Value string
}

func parseCommand(input string) (Command, bool) {
	normalized := strings.TrimPrefix(strings.TrimSpace(input), ":")
	if normalized == "" {
		return Command{}, false
	}
	parts := strings.Fields(normalized)
	if len(parts) == 0 {
		return Command{}, false
	}
	switch parts[0] {
	case "q", "quit", "q!":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "quit"}, true
	case "ns", "namespace":
		if len(parts) != 2 {
			return Command{}, false
		}
		return Command{Kind: "namespace", Value: parts[1]}, true
	case "ctx", "context", "contexts":
		if len(parts) == 2 {
			return Command{Kind: "context", Value: parts[1]}, true
		}
		if len(parts) == 1 {
			return Command{Kind: "palette"}, true
		}
		return Command{}, false
	case "all":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "namespace", Value: "all"}, true
	case "pf", "port-forward", "portforwards":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "port-forward-manager"}, true
	default:
		return Command{Kind: "resource", Value: parts[0]}, true
	}
}
