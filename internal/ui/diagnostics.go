package ui

import (
	"context"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/yaml"
	"strings"
)

func (a *App) showAccessReview() {
	client := a.options.Client
	namespace := client.Namespace
	if namespace == "" {
		a.showMessage("Access review", "Choose a namespace with :ns first")
		return
	}
	a.operation("Review permissions", func(ctx context.Context) (func(), error) {
		review, err := client.Clientset.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &authorizationv1.SelfSubjectRulesReview{Spec: authorizationv1.SelfSubjectRulesReviewSpec{Namespace: namespace}}, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		body, err := yaml.Marshal(review.Status)
		if err != nil {
			return nil, err
		}
		return func() { a.showDocument("Effective permissions · "+namespace, string(body)) }, nil
	})
}
func (a *App) showStorageDiagnostics() {
	obj := a.selectedObject()
	if obj == nil {
		return
	}
	obj = obj.DeepCopy()
	client := a.options.Client
	if obj.GetKind() != "PersistentVolumeClaim" && obj.GetKind() != "PersistentVolume" {
		a.showMessage("Storage diagnostics", "Select a PersistentVolume or PersistentVolumeClaim")
		return
	}
	a.operation("Storage diagnostics", func(ctx context.Context) (func(), error) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s/%s (%s)\n", obj.GetKind(), obj.GetName(), obj.GetNamespace())
		status, _, _ := unstructured.NestedMap(obj.Object, "status")
		data, _ := yaml.Marshal(status)
		b.Write(data)
		sc, _, _ := unstructured.NestedString(obj.Object, "spec", "storageClassName")
		if sc != "" {
			class, err := client.Clientset.StorageV1().StorageClasses().Get(ctx, sc, metav1.GetOptions{})
			if err != nil {
				fmt.Fprintf(&b, "\nStorageClass: %v\n", err)
			} else {
				fmt.Fprintf(&b, "\nStorageClass: %s\nProvisioner: %s\n", class.Name, class.Provisioner)
				if class.VolumeBindingMode != nil {
					fmt.Fprintf(&b, "Binding mode: %s\n", *class.VolumeBindingMode)
				}
			}
		}
		volume, _, _ := unstructured.NestedString(obj.Object, "spec", "volumeName")
		if volume != "" {
			pv, err := client.Clientset.CoreV1().PersistentVolumes().Get(ctx, volume, metav1.GetOptions{})
			if err != nil {
				fmt.Fprintf(&b, "\nBound volume: %v\n", err)
			} else {
				fmt.Fprintf(&b, "\nVolume %s: %s\nReclaim policy: %s\n", pv.Name, pv.Status.Phase, pv.Spec.PersistentVolumeReclaimPolicy)
				if pv.Spec.ClaimRef != nil && pv.Spec.ClaimRef.UID != obj.GetUID() {
					b.WriteString("WARNING: volume claim UID does not match this claim\n")
				}
			}
		}
		events, err := client.Clientset.CoreV1().Events(obj.GetNamespace()).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", string(obj.GetUID())).String()})
		if err != nil {
			fmt.Fprintf(&b, "\nEvents unavailable: %v", err)
		} else {
			b.WriteString("\nEvents:\n")
			for _, e := range events.Items {
				fmt.Fprintf(&b, "%s %s: %s\n", e.Type, e.Reason, e.Message)
			}
		}
		return func() { a.showDocument("Storage diagnostics", b.String()) }, nil
	})
}
func (a *App) showDocument(title, body string) {
	view := tview.NewTextView().SetScrollable(true).SetText(body)
	view.SetTitle(" " + tview.Escape(title) + " · Esc back ").SetBorder(true)
	view.SetTextColor(skinColor(a.skin.FG, tcell.ColorWhite))
	view.SetBackgroundColor(skinColor(a.skin.BG, tcell.ColorBlack))
	view.SetBorderColor(skinColor(a.skin.Subtle, tcell.ColorGray))
	view.SetTitleColor(skinColor(a.skin.Accent, tcell.ColorAqua))
	view.SetInputCapture(a.backCapture())
	a.app.SetRoot(view, true)
}
