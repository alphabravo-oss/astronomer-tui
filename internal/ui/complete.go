//revive:disable:exported package-comments
package ui

import (
	"sort"
	"strings"
)

// commandCompletions returns full ":"-prefixed candidate strings for the given
// prompt text, used by the command input's autocomplete dropdown.
func (a *App) commandCompletions(text string) []string {
	trimmed := strings.TrimPrefix(text, ":")
	if strings.TrimSpace(trimmed) == "" {
		return nil
	}
	fields := strings.Fields(trimmed)
	// Argument completion for commands that take a value.
	if len(fields) >= 1 && strings.Contains(trimmed, " ") {
		arg := ""
		if len(fields) >= 2 {
			arg = fields[len(fields)-1]
		}
		switch fields[0] {
		case "ns", "namespace":
			return fullCandidates(fields[0], a.namespaceNames(), arg)
		case "ctx", "context":
			return fullCandidates(fields[0], a.contextNames(), arg)
		}
		return nil
	}
	// First-token completion: commands + resource aliases.
	var cands []string
	cands = append(cands, commandTokens()...)
	for _, item := range ResourceAliases() {
		cands = append(cands, item.Aliases[len(item.Aliases)-1])
	}
	sort.Strings(cands)
	var out []string
	seen := map[string]struct{}{}
	for _, c := range cands {
		if strings.HasPrefix(c, trimmed) {
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, ":"+c)
		}
	}
	return out
}

func fullCandidates(cmd string, values []string, prefix string) []string {
	var out []string
	for _, v := range values {
		if strings.HasPrefix(v, prefix) {
			out = append(out, ":"+cmd+" "+v)
		}
	}
	sort.Strings(out)
	return out
}

// contextNames returns kube context names from the loaded kubeconfig.
func (a *App) contextNames() []string {
	if a.options.Client == nil {
		return nil
	}
	names := make([]string, 0, len(a.options.Client.RawConfig.Contexts))
	for name := range a.options.Client.RawConfig.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// pushHistory records an executed command (most-recent last), de-duplicating
// consecutive repeats and capping the list.
func (a *App) pushHistory(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := len(a.cmdHistory); n > 0 && a.cmdHistory[n-1] == cmd {
		a.histIdx = len(a.cmdHistory)
		return
	}
	a.cmdHistory = append(a.cmdHistory, cmd)
	if len(a.cmdHistory) > 100 {
		a.cmdHistory = a.cmdHistory[len(a.cmdHistory)-100:]
	}
	a.histIdx = len(a.cmdHistory)
}

// historyPrev/historyNext walk the command history for the : prompt.
func (a *App) historyPrev() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cmdHistory) == 0 {
		return "", false
	}
	if a.histIdx > 0 {
		a.histIdx--
	}
	return a.cmdHistory[a.histIdx], true
}

func (a *App) historyNext() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cmdHistory) == 0 {
		return "", false
	}
	if a.histIdx < len(a.cmdHistory)-1 {
		a.histIdx++
		return a.cmdHistory[a.histIdx], true
	}
	a.histIdx = len(a.cmdHistory)
	return "", true // past the end -> blank line
}
