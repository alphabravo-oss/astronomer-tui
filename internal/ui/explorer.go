package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (a *App) stopAuxiliary() {
	if a.viewCancel != nil {
		a.viewCancel()
		a.viewCancel = nil
	}
}
func (a *App) auxiliaryContext() context.Context {
	a.stopAuxiliary()
	ctx, cancel := context.WithCancel(a.backgroundContext())
	a.viewCancel = cancel
	return ctx
}
func (a *App) explorerTable(title string, headers []string) *tview.Table {
	t := tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	t.SetTitle(" " + tview.Escape(title) + " ").SetBorder(true)
	t.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	t.SetBorderColor(skinColor(a.skin.Subtle, tcell.ColorGray))
	t.SetTitleColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	t.SetSelectedStyle(tcell.StyleDefault.Foreground(skinColor(a.skin.FG, tcell.ColorWhite)).Background(skinColor(a.skin.Selection, tcell.ColorBlue)))
	for i, h := range headers {
		t.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(skinColor(a.skin.Accent, tcell.ColorAqua)))
	}
	return t
}
func (a *App) setExplorerRow(t *tview.Table, row int, values ...string) {
	for i, v := range values {
		t.SetCell(row, i, tview.NewTableCell(tview.Escape(v)).SetTextColor(skinColor(a.skin.FG, tcell.ColorWhite)))
	}
}

func (a *App) showPulses() {
	client := a.options.Client
	if client == nil || client.Dynamic == nil {
		a.showMessage("Pulses", "Kubernetes client unavailable")
		return
	}
	ns := client.Namespace
	ctx := a.auxiliaryContext()
	table := a.explorerTable("Pulses · loading · Esc back", []string{"RESOURCE", "TOTAL", "HEALTHY", "WARNING", "UNKNOWN", "WARN HISTORY", "STATUS"})
	var current []cluster.HealthCount
	history := map[string][]int{}
	open := func(faults bool) {
		row, _ := table.GetSelection()
		if row < 1 || row > len(current) {
			return
		}
		c := current[row-1]
		if c.Err != nil {
			a.showMessage("Pulses", c.Err.Error())
			a.stopAuxiliary()
			return
		}
		targetNS := ns
		if !c.Namespaced {
			targetNS = ""
		}
		var p func(*unstructured.Unstructured) bool
		if faults {
			p = func(u *unstructured.Unstructured) bool { return cluster.ResourceHealth(u) != "healthy" }
		}
		a.relatedView(c.GVR, targetNS, p)
	}
	table.SetSelectedFunc(func(int, int) { open(false) })
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'f' {
			open(true)
			return nil
		}
		return e
	})
	a.app.SetRoot(table, true).SetFocus(table)
	go func() {
		for {
			poll, stop := context.WithTimeout(ctx, 10*time.Second)
			counts := cluster.ReadHealth(poll, client.Dynamic, ns)
			usage, metricsErr := cluster.FetchMetrics(poll, client.Dynamic, "pods", ns)
			stop()
			var cpu, mem int64
			stale := false
			for _, u := range usage {
				cpu += u.CPUMilli
				mem += u.MemoryBytes
				stale = stale || u.Timestamp.IsZero() || time.Since(u.Timestamp) > 2*time.Minute
			}
			a.queueUpdate(ctx, func() {
				current = counts
				if row, _ := table.GetSelection(); row == 0 {
					table.Select(1, 0)
				}
				for i, c := range counts {
					h := append(history[c.GVR.Resource], c.Warning)
					if len(h) > 40 {
						h = h[len(h)-40:]
					}
					history[c.GVR.Resource] = h
					var graph strings.Builder
					for _, n := range h {
						if n == 0 {
							graph.WriteByte('_')
						} else if n < 10 {
							graph.WriteByte(byte('0' + n))
						} else {
							graph.WriteByte('+')
						}
					}
					status := "ok"
					total, healthy, warn, unknown := fmt.Sprint(c.Total), fmt.Sprint(c.Healthy), fmt.Sprint(c.Warning), fmt.Sprint(c.Unknown)
					if c.Err != nil {
						status = c.Err.Error()
						total = "?"
						healthy = "?"
						warn = "?"
						unknown = "?"
					}
					a.setExplorerRow(table, i+1, c.GVR.Resource, total, healthy, warn, unknown, graph.String(), status)
					if c.Warning > 0 || c.Err != nil {
						for _, col := range []int{3, 5, 6} {
							table.GetCell(i+1, col).SetTextColor(skinColor(a.skin.Warn, tcell.ColorYellow))
						}
					}
					if c.Healthy > 0 {
						table.GetCell(i+1, 2).SetTextColor(skinColor(a.skin.Accent, tcell.ColorAqua))
					}
				}
				metrics := fmt.Sprintf("pod usage %dm / %dMi", cpu, mem/(1024*1024))
				if metricsErr != nil {
					metrics = "metrics unavailable"
				} else if stale {
					metrics += " (stale)"
				}
				scope := ns
				if scope == "" {
					scope = "all namespaces"
				}
				table.SetTitle(" Pulses · " + tview.Escape(scope) + " · " + metrics + " · Enter browse · f faults · Esc back ")
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func graphChildren(parent cluster.GraphObject, all []cluster.GraphObject) []cluster.GraphObject {
	p := parent.Object
	var out []cluster.GraphObject
	for _, child := range all {
		c := child.Object
		if c.GetNamespace() != p.GetNamespace() && p.GetKind() != "Node" {
			continue
		}
		if c.GetUID() != "" && c.GetUID() == p.GetUID() {
			continue
		}
		linked := false
		if p.GetUID() != "" {
			for _, owner := range c.GetOwnerReferences() {
				if owner.UID == p.GetUID() {
					linked = true
				}
			}
		}
		switch p.GetKind() {
		case "Service":
			if c.GetKind() == "Pod" {
				selector, _, _ := unstructured.NestedStringMap(p.Object, "spec", "selector")
				linked = len(selector) > 0 && labels.SelectorFromSet(selector).Matches(labels.Set(c.GetLabels()))
			}
			if c.GetKind() == "EndpointSlice" {
				linked = c.GetLabels()["kubernetes.io/service-name"] == p.GetName()
			}
		case "Node":
			if c.GetKind() == "Pod" {
				node, _, _ := unstructured.NestedString(c.Object, "spec", "nodeName")
				linked = node == p.GetName()
			}
		case "Pod":
			if c.GetKind() == "PersistentVolumeClaim" {
				volumes, _, _ := unstructured.NestedSlice(p.Object, "spec", "volumes")
				for _, raw := range volumes {
					v, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					name, _, _ := unstructured.NestedString(v, "persistentVolumeClaim", "claimName")
					if name == c.GetName() {
						linked = true
					}
				}
			}
		}
		if linked {
			out = append(out, child)
		}
	}
	return out
}
func (a *App) showXRay(resource, namespace string) {
	if resource == "" {
		resource = cluster.Qualified(a.currentGVR)
	}
	g, err := a.resolveResource(resource)
	if err != nil {
		a.showMessage("XRay", err.Error())
		return
	}
	if namespace == "" {
		namespace = a.options.Client.Namespace
	}
	if namespace == "all" || namespace == "*" {
		namespace = ""
	}
	client := a.options.Client
	ns := namespace
	a.operation("Load XRay", func(ctx context.Context) (func(), error) {
		snapshot := cluster.ReadGraph(ctx, client.Dynamic, g, ns)
		return func() { a.renderXRay(g, ns, snapshot) }, nil
	})
}
func (a *App) renderXRay(g schema.GroupVersionResource, ns string, snapshot cluster.GraphSnapshot) {
	root := tview.NewTreeNode("XRay · " + g.Resource + " · " + ns).SetExpanded(true)
	tree := tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	tree.SetBorder(true).SetTitle(" XRay · Enter browse · Space expand · r refresh · Esc back ")
	tree.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	tree.SetBorderColor(skinColor(a.skin.Subtle, tcell.ColorGray))
	tree.SetTitleColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	root.SetColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	count := 0
	var add func(*tview.TreeNode, cluster.GraphObject, map[string]bool, int)
	add = func(parent *tview.TreeNode, obj cluster.GraphObject, path map[string]bool, depth int) {
		if count >= 10000 || depth > 12 {
			return
		}
		key := string(obj.Object.GetUID())
		if key == "" {
			key = obj.GVR.String() + "/" + obj.Object.GetNamespace() + "/" + obj.Object.GetName()
		}
		if path[key] {
			return
		}
		path[key] = true
		defer delete(path, key)
		count++
		label := obj.Object.GetKind() + "/" + obj.Object.GetName()
		if obj.Object.GetNamespace() != "" {
			label = obj.Object.GetNamespace() + "/" + label
		}
		node := tview.NewTreeNode(tview.Escape(label)).SetReference(obj).SetColor(skinColor(a.skin.FG, tcell.ColorWhite)).SetExpanded(true)
		if cluster.ResourceHealth(obj.Object) == "warning" {
			node.SetColor(skinColor(a.skin.Warn, tcell.ColorYellow))
		}
		parent.AddChild(node)
		if obj.Object.GetKind() == "Pod" {
			for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
				containers, _, _ := unstructured.NestedSlice(obj.Object.Object, "spec", field)
				for _, raw := range containers {
					m, ok := raw.(map[string]any)
					if ok {
						node.AddChild(tview.NewTreeNode(tview.Escape(fmt.Sprint(field, "/", m["name"], " · ", m["image"]))).SetColor(skinColor(a.skin.Subtle, tcell.ColorGray)))
					}
				}
			}
		}
		for _, child := range graphChildren(obj, snapshot.Objects) {
			add(node, child, path, depth+1)
		}
	}
	for _, obj := range snapshot.Objects {
		if obj.GVR == g {
			add(root, obj, map[string]bool{}, 0)
		}
	}
	for _, warning := range snapshot.Warnings {
		root.AddChild(tview.NewTreeNode(tview.Escape("INCOMPLETE: " + warning)).SetColor(skinColor(a.skin.Warn, tcell.ColorYellow)))
	}
	if count == 0 {
		root.AddChild(tview.NewTreeNode("No visible roots in this scope"))
	}
	if count >= 10000 {
		root.AddChild(tview.NewTreeNode("Display capped at 10,000 graph nodes"))
	}
	tree.SetSelectedFunc(func(n *tview.TreeNode) {
		if obj, ok := n.GetReference().(cluster.GraphObject); ok {
			uid := obj.Object.GetUID()
			name := obj.Object.GetName()
			a.relatedView(obj.GVR, obj.Object.GetNamespace(), func(u *unstructured.Unstructured) bool { return uid != "" && u.GetUID() == uid && u.GetName() == name })
		} else {
			n.SetExpanded(!n.IsExpanded())
		}
	})
	tree.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEsc {
			a.showMain()
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'r' {
			scope := ns
			if scope == "" {
				scope = "all"
			}
			a.showXRay(cluster.Qualified(g), scope)
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == ' ' {
			n := tree.GetCurrentNode()
			n.SetExpanded(!n.IsExpanded())
			return nil
		}
		return e
	})
	a.app.SetRoot(tree, true).SetFocus(tree)
}

func (a *App) showRBAC(args []string) {
	client := a.options.Client
	ns := client.Namespace
	a.operation("Load RBAC bindings", func(ctx context.Context) (func(), error) {
		snapshot := cluster.ReadRBAC(ctx, client.Clientset, ns)
		return func() {
			if len(args) == 0 {
				a.showRBACSubjects(snapshot, "")
				return
			}
			kind := map[string]string{"user": "User", "users": "User", "group": "Group", "groups": "Group", "sa": "ServiceAccount", "serviceaccount": "ServiceAccount", "serviceaccounts": "ServiceAccount"}[args[0]]
			if kind == "" {
				a.showMessage("RBAC", "Usage: :rbac [user|group|sa NAME [namespace-for-sa or user-groups-comma-separated]]")
				return
			}
			if len(args) == 1 {
				a.showRBACSubjects(snapshot, kind)
				return
			}
			subject := rbacv1.Subject{Kind: kind, Name: args[1]}
			var groups []string
			if kind == "ServiceAccount" {
				subject.Namespace = ns
				if len(args) > 2 {
					subject.Namespace = args[2]
				}
			} else if len(args) > 2 {
				groups = strings.Split(args[2], ",")
			}
			a.showDocument("RBAC · "+cluster.SubjectName(subject), cluster.FormatGrants(snapshot.ForSubject(subject, groups), snapshot.Warnings))
		}, nil
	})
}
func (a *App) showRBACSubjects(snapshot cluster.RBACSnapshot, kind string) {
	subjects := snapshot.Subjects(kind)
	table := a.explorerTable("RBAC subjects · Enter grants · Esc back", []string{"KIND", "NAMESPACE", "NAME"})
	for i, s := range subjects {
		a.setExplorerRow(table, i+1, s.Kind, s.Namespace, s.Name)
	}
	if len(snapshot.Warnings) > 0 {
		table.SetTitle(" RBAC · INCOMPLETE visibility · Enter grants · Esc back ")
	}
	if len(subjects) == 0 {
		a.setExplorerRow(table, 1, "No visible subjects", "", strings.Join(snapshot.Warnings, "; "))
	}
	table.SetSelectedFunc(func(row, col int) {
		if row > 0 && row <= len(subjects) {
			s := subjects[row-1]
			a.showDocument("RBAC · "+cluster.SubjectName(s), cluster.FormatGrants(snapshot.ForSubject(s, nil), snapshot.Warnings))
		}
	})
	table.SetInputCapture(a.backCapture())
	a.app.SetRoot(table, true).SetFocus(table)
}
func (a *App) showWhoCan(args []string) {
	if len(args) < 2 || len(args) > 4 {
		a.showMessage("Who can", "Usage: :who-can VERB RESOURCE[/SUBRESOURCE] [NAMESPACE] [NAME]")
		return
	}
	resource, sub, _ := strings.Cut(args[1], "/")
	g, err := a.resolveResource(resource)
	if err != nil {
		a.showMessage("Who can", err.Error())
		return
	}
	baseGVR := g
	if sub != "" {
		g.Resource += "/" + sub
	}
	ns := a.options.Client.Namespace
	if len(args) > 2 {
		ns = args[2]
	}
	if ns == "all" || ns == "*" {
		ns = ""
	}
	if r, ok := a.catalog.Lookup(baseGVR); (ok && !r.Namespaced) || (!ok && namespaceFor(a.options.Client, baseGVR) == "" && a.options.Client.Namespace != "") {
		ns = ""
	}
	name := ""
	if len(args) > 3 {
		name = args[3]
	}
	client := a.options.Client
	a.operation("Reverse RBAC lookup", func(ctx context.Context) (func(), error) {
		s := cluster.ReadRBAC(ctx, client.Clientset, ns)
		grants := s.WhoCan(args[0], g.Group, g.Resource, ns, name)
		return func() {
			a.showDocument("Who can · "+strings.Join(args, " "), cluster.FormatGrants(grants, s.Warnings))
		}, nil
	})
}
