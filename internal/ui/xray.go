//revive:disable:exported package-comments
package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravo-oss/astrotui/internal/ui/views"
)

// xrayNode is a node in the dependency tree.
type xrayNode struct {
	label    string
	color    string
	children []xrayNode
}

var (
	rsGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// xrayTree builds the ownerRef dependency tree for the given workload kind.
// Pure for testing: callers pass the relevant cached object lists.
func xrayTree(kind string, roots, replicaSets, pods []*unstructured.Unstructured) []xrayNode {
	podsByOwner := indexByOwner(pods)
	rsByOwner := indexByOwner(replicaSets)
	rootGVR := workloadGVR(kind)

	nodes := make([]xrayNode, 0, len(roots))
	for _, root := range roots {
		node := xrayNode{
			label: fmt.Sprintf("%s/%s", kind, root.GetName()),
			color: views.StateColor(rootGVR, root),
		}
		switch kind {
		case "Deployment":
			for _, rs := range rsByOwner[root.GetUID()] {
				rsNode := xrayNode{label: "ReplicaSet/" + rs.GetName(), color: views.StateColor(rsGVR, rs)}
				for _, pod := range podsByOwner[rs.GetUID()] {
					rsNode.children = append(rsNode.children, podXrayNode(pod))
				}
				node.children = append(node.children, rsNode)
			}
		default: // StatefulSet, DaemonSet
			for _, pod := range podsByOwner[root.GetUID()] {
				node.children = append(node.children, podXrayNode(pod))
			}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func podXrayNode(pod *unstructured.Unstructured) xrayNode {
	node := xrayNode{label: "Pod/" + pod.GetName(), color: views.StateColor(podGVR, pod)}
	statuses := containerStatusIndex(pod)
	for _, name := range containerNames(pod) {
		color := "error"
		if statuses[name].ready {
			color = "ok"
		}
		node.children = append(node.children, xrayNode{label: "Container/" + name, color: color})
	}
	return node
}

func indexByOwner(objs []*unstructured.Unstructured) map[types.UID][]*unstructured.Unstructured {
	idx := map[types.UID][]*unstructured.Unstructured{}
	for _, o := range objs {
		for _, ref := range o.GetOwnerReferences() {
			idx[ref.UID] = append(idx[ref.UID], o)
		}
	}
	return idx
}

func workloadGVR(kind string) schema.GroupVersionResource {
	switch kind {
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	case "StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	case "DaemonSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	default:
		return schema.GroupVersionResource{}
	}
}

// showXrayView renders the dependency tree for a workload kind.
func (a *App) showXrayView(ctx context.Context, kindArg string) {
	kind := xrayKind(kindArg)
	if kind == "" {
		a.Flash(FlashError, "xray supports deploy, sts, ds")
		return
	}
	if a.cache == nil {
		a.Flash(FlashError, "cache not ready")
		return
	}
	list := func(gvr schema.GroupVersionResource) []*unstructured.Unstructured {
		a.cache.Ensure(gvr)
		syncCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		a.cache.WaitForSync(syncCtx, gvr)
		ns := ""
		if a.options.Client != nil {
			ns = a.options.Client.Namespace
		}
		items, _ := a.cache.List(gvr, ns, labels.Everything())
		return items
	}
	roots := list(workloadGVR(kind))
	var rss []*unstructured.Unstructured
	if kind == "Deployment" {
		rss = list(rsGVR)
	}
	pods := list(podGVR)
	tree := xrayTree(kind, roots, rss, pods)

	root := tview.NewTreeNode(kind + " xray").SetSelectable(false)
	for _, n := range tree {
		root.AddChild(xrayTreeNode(a, n))
	}
	view := tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	view.SetTitle(" xray: " + kind + " ").SetBorder(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' {
			a.back()
			return nil
		}
		return event
	})
	a.pushView(view, view, "xray:"+kind, nil)
}

func xrayTreeNode(a *App, n xrayNode) *tview.TreeNode {
	node := tview.NewTreeNode(n.label)
	if n.color != "" {
		node.SetColor(a.semanticColor(n.color))
	}
	for _, c := range n.children {
		node.AddChild(xrayTreeNode(a, c))
	}
	if len(n.children) > 0 {
		node.SetExpanded(true)
	}
	return node
}

func xrayKind(arg string) string {
	switch arg {
	case "deploy", "deployment", "deployments", "dp":
		return "Deployment"
	case "sts", "statefulset", "statefulsets":
		return "StatefulSet"
	case "ds", "daemonset", "daemonsets":
		return "DaemonSet"
	default:
		return ""
	}
}
