package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func (a *App) specialCommand(command string) bool {
	args := strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), ":"))
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "workloads", "wk":
		a.showWorkloads()
	case "references", "ref":
		a.showReferences()
	case "scan", "scans":
		a.showImageScan()
	case "kill":
		a.deleteMarked(true)
	case "containers", "co":
		a.showContainers()
	case "node-shell":
		a.showNodeShell()
	case "helm", "charts":
		a.showHelm()
	case "pulses", "pu":
		a.showPulses()
	case "xray":
		resource, ns := "", ""
		if len(args) > 1 {
			resource = args[1]
		}
		if len(args) > 2 {
			ns = args[2]
		}
		a.showXRay(resource, ns)
	case "rbac":
		a.showRBAC(args[1:])
	case "users", "user":
		a.showRBAC(append([]string{"user"}, args[1:]...))
	case "groups", "group":
		a.showRBAC(append([]string{"group"}, args[1:]...))
	case "who-can":
		a.showWhoCan(args[1:])
	case "bench", "benchmark":
		endpoint := ""
		if len(args) > 1 {
			endpoint = args[1]
		}
		a.showBenchmark(endpoint)
	case "be", "benchmarks":
		a.showArchives("benchmarks")
	case "sd", "screendump", "screendumps":
		a.showArchives("screendumps")
	case "save":
		a.saveSelectedResource()
	case "decode":
		a.showDecodedSecret()
	case "aliases", "alias":
		a.showAliasList()
	default:
		return false
	}
	return true
}
func (a *App) showAliasList() {
	var lines []string
	if a.catalog != nil {
		for _, r := range a.catalog.Resources {
			names := append([]string{r.GVR.Resource, r.Kind, r.Singular}, r.ShortNames...)
			lines = append(lines, fmt.Sprintf("%-45s %s", cluster.Qualified(r.GVR), strings.Join(names, " ")))
		}
	}
	for name, command := range a.custom.Aliases {
		lines = append(lines, name+" -> "+command)
	}
	sort.Strings(lines)
	lines = append([]string{"pulses/pu · xray · rbac · users · groups · who-can · bench · benchmarks/be · screendumps/sd · save · decode · aliases", ""}, lines...)
	a.showDocument("Resource aliases and views", strings.Join(lines, "\n"))
}

func parityHelp() string {
	return `
Core explorers:
  :workloads / :wk              Combined workload inventory
  :references / :ref           Pod dependencies or consumers of the selected resource
  Space / Ctrl-D / Ctrl-K        Mark resources / delete marked / confirmed grace-zero delete
  :scan                         Explicit image vulnerability scan (requires grype)
  :containers / :co             Selected pod containers, env/status, logs and shell
  :node-shell                   Confirmed privileged node shell with helper cleanup
  :helm / :charts                Releases, history, values, manifests, rollback/uninstall
  :pulses / :pu                  Live health and usage; Enter browse, f faults
  :xray [RESOURCE] [NAMESPACE]    Ownership/selector tree; Enter browse, Space expand
  :rbac [user|group|sa NAME]      Visible subject grants and binding provenance
  :rbac user NAME GROUP1,GROUP2   Include known group membership
  :rbac sa NAME NAMESPACE        Service-account and standard group grants
  :who-can VERB RESOURCE [NS] [NAME]  Reverse RBAC binding lookup
  :bench [URL]                   Configure and explicitly start HTTP load
  :be                            Saved benchmark reports
  :save / Ctrl-S                 Save selected resource (Secret exports confirm)
  :sd                            Open saved resource captures
  :decode / x on Secret          Decode selected Secret locally
  :aliases / Ctrl-A              Resource aliases and custom commands
`
}

// Append auxiliary view commands to the palette without pretending they are APIs.
var explorerCommands = []string{"workloads", "references", "scan", "containers", "node-shell", "helm", "pulses", "xray", "rbac", "users", "groups", "bench", "benchmarks", "screendumps", "aliases", "decode", "save"}
