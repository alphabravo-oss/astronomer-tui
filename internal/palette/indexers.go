//revive:disable:exported package-comments
package palette

import (
	"fmt"

	"github.com/alphabravo-oss/astrotui/internal/plugins"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ResourceEntries(kind string, objects []*unstructured.Unstructured) []Entry {
	entries := make([]Entry, 0, len(objects))
	for _, obj := range objects {
		title := fmt.Sprintf("%s/%s/%s", kind, obj.GetNamespace(), obj.GetName())
		if obj.GetNamespace() == "" {
			title = fmt.Sprintf("%s/%s", kind, obj.GetName())
		}
		entries = append(entries, Entry{ID: title, Title: title, Category: Resources, Payload: obj})
	}
	return entries
}

func ActionEntries(actions []string) []Entry {
	entries := make([]Entry, 0, len(actions))
	for _, action := range actions {
		entries = append(entries, Entry{ID: "action:" + action, Title: action, Category: Actions})
	}
	return entries
}

func ContextEntries(contexts []string) []Entry {
	entries := make([]Entry, 0, len(contexts))
	for _, ctx := range contexts {
		entries = append(entries, Entry{ID: "context:" + ctx, Title: ctx, Category: Contexts})
	}
	return entries
}

func NamespaceEntries(namespaces []string) []Entry {
	entries := make([]Entry, 0, len(namespaces))
	for _, namespace := range namespaces {
		entries = append(entries, Entry{ID: "namespace:" + namespace, Title: namespace, Category: Namespaces})
	}
	return entries
}

func PluginEntries(file plugins.File) []Entry {
	entries := make([]Entry, 0, len(file.Plugins))
	for name, plugin := range file.Plugins {
		entries = append(entries, Entry{
			ID:          "plugin:" + name,
			Title:       name,
			Description: plugin.Description,
			Category:    Plugins,
			Payload:     plugin,
		})
	}
	return entries
}
