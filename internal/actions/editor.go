//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type Editor interface {
	Edit(path string) error
}

type CommandEditor struct {
	Command string
	Args    []string
}

type EditConflictError struct {
	Err  error
	Diff string
}

type EditPlan struct {
	Object   *unstructured.Unstructured
	Original []byte
	Edited   []byte
	Patch    []byte
	Diff     string
}

func (e *EditConflictError) Error() string {
	return "edit conflict: " + e.Err.Error()
}

func (e *EditConflictError) Unwrap() error {
	return e.Err
}

func DefaultEditor() CommandEditor {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	return CommandEditor{Command: editor}
}

func (e CommandEditor) Edit(path string) error {
	args := append([]string{}, e.Args...)
	args = append(args, path)
	cmd := exec.Command(e.Command, args...) // #nosec G204 -- editor command is explicitly user-selected through $EDITOR/config.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func EditResource(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, obj *unstructured.Unstructured, editor Editor) (*unstructured.Unstructured, error) {
	plan, err := PrepareEdit(obj, editor)
	if err != nil {
		return nil, err
	}
	return ApplyEditPlan(ctx, client, gvr, plan)
}

func PrepareEdit(obj *unstructured.Unstructured, editor Editor) (EditPlan, error) {
	if editor == nil {
		defaultEditor := DefaultEditor()
		editor = defaultEditor
	}
	original, err := YAMLForEdit(obj)
	if err != nil {
		return EditPlan{}, err
	}
	path, err := writeTempYAML(obj.GetName(), original)
	if err != nil {
		return EditPlan{}, err
	}
	defer func() { _ = os.Remove(path) }()
	if err := editor.Edit(path); err != nil {
		return EditPlan{}, err
	}
	edited, err := os.ReadFile(path) // #nosec G304 -- path is a tempfile created by this function.
	if err != nil {
		return EditPlan{}, err
	}
	patch, err := StrategicMergePatch(original, edited)
	if err != nil {
		return EditPlan{}, err
	}
	return EditPlan{Object: obj, Original: original, Edited: edited, Patch: patch, Diff: simpleDiff(original, edited)}, nil
}

func PreviewEditPlan(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, plan EditPlan) error {
	if len(plan.Patch) == 0 {
		return nil
	}
	return PatchResourceDryRun(ctx, client, gvr, plan.Object.GetNamespace(), plan.Object.GetName(), plan.Patch)
}

func ApplyEditPlan(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, plan EditPlan) (*unstructured.Unstructured, error) {
	if len(plan.Patch) == 0 {
		return plan.Object, nil
	}
	updated, err := PatchResource(ctx, client, gvr, plan.Object.GetNamespace(), plan.Object.GetName(), plan.Patch)
	if err != nil {
		if apierrors.IsConflict(err) {
			return nil, &EditConflictError{Err: err, Diff: plan.Diff}
		}
		return nil, err
	}
	return updated, nil
}

func simpleDiff(original, edited []byte) string {
	oldLines := strings.Split(strings.TrimRight(string(original), "\n"), "\n")
	newLines := strings.Split(strings.TrimRight(string(edited), "\n"), "\n")
	var b strings.Builder
	b.WriteString("--- original\n+++ edited\n")
	maxLines := max(len(oldLines), len(newLines))
	for i := range maxLines {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine == newLine {
			continue
		}
		if i < len(oldLines) {
			b.WriteString("- ")
			b.WriteString(oldLine)
			b.WriteByte('\n')
		}
		if i < len(newLines) {
			b.WriteString("+ ")
			b.WriteString(newLine)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeTempYAML(name string, data []byte) (string, error) {
	if name == "" {
		name = "resource"
	}
	file, err := os.CreateTemp("", "astrotui-"+filepath.Base(name)+"-*.yaml")
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("write temp yaml: %w", err)
	}
	return file.Name(), nil
}
