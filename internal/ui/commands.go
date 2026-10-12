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
	case "access", "diagnose":
		return Command{Kind: parts[0]}, len(parts) == 1
	case "refresh":
		return Command{Kind: "refresh"}, len(parts) == 1
	case "ns", "namespace":
		if len(parts) > 2 {
			return Command{}, false
		}
		if len(parts) == 1 {
			return Command{Kind: "namespace"}, true
		}
		return Command{Kind: "namespace", Value: parts[1]}, true
	case "ctx", "context":
		if len(parts) > 2 {
			return Command{}, false
		}
		if len(parts) == 1 {
			return Command{Kind: "context"}, true
		}
		return Command{Kind: "context", Value: parts[1]}, true
	case "q", "quit":
		return Command{Kind: "quit"}, len(parts) == 1
	case "pf", "port-forward", "portforwards":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "port-forward-manager"}, true
	default:
		if len(parts) > 16 {
			return Command{}, false
		}
		return Command{Kind: "resource", Value: strings.Join(parts, " ")}, true
	}
}
