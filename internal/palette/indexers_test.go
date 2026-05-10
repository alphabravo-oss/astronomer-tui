package palette

import (
	"testing"

	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIndexers(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetName("api")
	obj.SetNamespace("default")
	entries := ResourceEntries("pod", []*unstructured.Unstructured{obj})
	entries = append(entries, ActionEntries([]string{"restart"})...)
	entries = append(entries, ContextEntries([]string{"kind-kind"})...)
	entries = append(entries, NamespaceEntries([]string{"default"})...)
	entries = append(entries, PluginEntries(plugins.File{Plugins: map[string]plugins.Plugin{"stern": {Description: "Tail logs"}}})...)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}
