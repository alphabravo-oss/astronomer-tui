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
	case "ns", "namespace":
		if len(parts) != 2 {
			return Command{}, false
		}
		return Command{Kind: "namespace", Value: parts[1]}, true
	case "pf", "port-forward", "portforwards":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "port-forward-manager"}, true
	case "auth", "whoami":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "auth-summary"}, true
	case "health", "pulses":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "health"}, true
	case "be", "bench", "benchmarks":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "benchmark-manager"}, true
	default:
		return Command{Kind: "resource", Value: parts[0]}, true
	}
}
