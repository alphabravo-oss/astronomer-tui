//revive:disable:exported package-comments
package ui

import (
	"strings"
)

type Command struct {
	Kind  string
	Value string
}

// commandSpec documents a command for the help overlay and autocomplete. The
// dispatch itself lives in parseCommand; this catalog is the single source of
// truth for how commands are described.
type commandSpec struct {
	usage string
	help  string
}

// commandTokens are the built-in command keywords (first token after ":").
func commandTokens() []string {
	return []string{"q", "quit", "ns", "ctx", "all", "pf", "flash", "alias"}
}

func commandCatalog() []commandSpec {
	return []commandSpec{
		{usage: ":<resource>", help: "Switch view to any resource/alias/CRD"},
		{usage: ":ns <name>|all", help: "Switch namespace (all = every namespace)"},
		{usage: ":ctx [name]", help: "Switch context, or open the context palette"},
		{usage: ":all", help: "Show all namespaces"},
		{usage: ":pf", help: "Port-forward manager"},
		{usage: ":pulses", help: "Cluster health tiles"},
		{usage: ":xray <kind>", help: "Dependency tree (deploy/sts/ds)"},
		{usage: ":ai", help: "Cluster-aware AI chat"},
		{usage: ":rbac u:|g:|s:", help: "Aggregate a subject's RBAC rules"},
		{usage: ":can <verb> <res>", help: "Check access (SelfSubjectAccessReview)"},
		{usage: ":alias", help: "List aliases and commands"},
		{usage: ":flash", help: "Recent status messages"},
		{usage: ":q", help: "Quit astrotui"},
	}
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
			return Command{Kind: "contexts-view"}, true
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
	case "flash", "flashes":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "flash"}, true
	case "alias", "aliases":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "aliases-view"}, true
	case "pulses", "pulse":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "pulses"}, true
	case "xray":
		if len(parts) != 2 {
			return Command{}, false
		}
		return Command{Kind: "xray", Value: parts[1]}, true
	case "ai":
		if len(parts) != 1 {
			return Command{}, false
		}
		return Command{Kind: "ai"}, true
	case "rbac":
		if len(parts) != 2 {
			return Command{}, false
		}
		return Command{Kind: "rbac", Value: parts[1]}, true
	case "can":
		if len(parts) < 3 {
			return Command{}, false
		}
		return Command{Kind: "can", Value: parts[1] + " " + parts[2]}, true
	default:
		return Command{Kind: "resource", Value: parts[0]}, true
	}
}
