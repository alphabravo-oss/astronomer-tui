package ui

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func pod(phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"phase": phase}}}
}

func dep(ready, desired int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"replicas": desired},
		"status": map[string]any{"readyReplicas": ready},
	}}
}

func nodeReadyObj(ready bool) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status}}},
	}}
}

func warnEvent() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"type": "Warning"}}
}

func TestPulseTiles(t *testing.T) {
	pods := []*unstructured.Unstructured{pod("Running"), pod("Running"), pod("Pending")}
	deps := []*unstructured.Unstructured{dep(3, 3), dep(1, 2)}
	nodes := []*unstructured.Unstructured{nodeReadyObj(true), nodeReadyObj(false)}
	events := []*unstructured.Unstructured{warnEvent(), warnEvent()}

	tiles := pulseTiles(pods, deps, nodes, events, 42, 80, true)

	byLabel := map[string]pulseTile{}
	for _, tile := range tiles {
		byLabel[tile.Label] = tile
	}
	if byLabel["Pods"].Value != "2/3" || byLabel["Pods"].Color != "warn" {
		t.Fatalf("unexpected pods tile %#v", byLabel["Pods"])
	}
	if byLabel["Deployments"].Value != "1/2" || byLabel["Deployments"].Color != "warn" {
		t.Fatalf("unexpected deployments tile %#v", byLabel["Deployments"])
	}
	if byLabel["Nodes"].Value != "1/2" {
		t.Fatalf("unexpected nodes tile %#v", byLabel["Nodes"])
	}
	if byLabel["Warnings"].Value != "2" || byLabel["Warnings"].Color != "warn" {
		t.Fatalf("unexpected warnings tile %#v", byLabel["Warnings"])
	}
	if byLabel["Cluster CPU"].Value != "42%" || byLabel["Cluster CPU"].Color != "ok" {
		t.Fatalf("unexpected cpu tile %#v", byLabel["Cluster CPU"])
	}
	if byLabel["Cluster MEM"].Value != "80%" || byLabel["Cluster MEM"].Color != "warn" {
		t.Fatalf("unexpected mem tile %#v", byLabel["Cluster MEM"])
	}
}

func TestPulseTilesNoMetrics(t *testing.T) {
	tiles := pulseTiles(nil, nil, nil, nil, 0, 0, false)
	for _, tile := range tiles {
		if tile.Label == "Cluster CPU" || tile.Label == "Cluster MEM" {
			t.Fatal("cluster gauges should be omitted without metrics")
		}
	}
}

func TestParsePulsesAndRBACCommands(t *testing.T) {
	if cmd, ok := parseCommand(":pulses"); !ok || cmd.Kind != "pulses" {
		t.Fatalf("unexpected pulses command %#v %v", cmd, ok)
	}
	if cmd, ok := parseCommand(":rbac u:alice"); !ok || cmd.Kind != "rbac" || cmd.Value != "u:alice" {
		t.Fatalf("unexpected rbac command %#v %v", cmd, ok)
	}
	if cmd, ok := parseCommand(":can get pods"); !ok || cmd.Kind != "can" || cmd.Value != "get pods" {
		t.Fatalf("unexpected can command %#v %v", cmd, ok)
	}
}
