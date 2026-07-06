//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// SetNodeSchedulable cordons (unschedulable=true) or uncordons a node.
func SetNodeSchedulable(ctx context.Context, client kubernetes.Interface, name string, unschedulable bool) error {
	patch := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, unschedulable)
	_, err := client.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// DrainOptions controls a drain operation.
type DrainOptions struct {
	GracePeriodSeconds int64
	Timeout            time.Duration
	IgnoreDaemonSets   bool
	DeleteEmptyDirData bool
}

// DrainResult summarizes a drain.
type DrainResult struct {
	Evicted int
	Skipped int
}

// DrainNode cordons a node then evicts its pods, skipping mirror pods and
// (optionally) DaemonSet pods. It reports progress via the onProgress callback
// and honors the timeout. Pods without a controller are evicted unless they are
// mirror pods.
func DrainNode(ctx context.Context, client kubernetes.Interface, name string, opts DrainOptions, onProgress func(string)) (DrainResult, error) {
	var result DrainResult
	if err := SetNodeSchedulable(ctx, client, name, true); err != nil {
		return result, fmt.Errorf("cordon: %w", err)
	}
	progress := func(format string, args ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, args...))
		}
	}
	progress("cordoned %s", name)

	fieldSel := fields.OneTermEqualSelector("spec.nodeName", name).String()
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: fieldSel})
	if err != nil {
		return result, fmt.Errorf("list pods: %w", err)
	}

	deadline := time.Now().Add(opts.Timeout)
	if opts.Timeout <= 0 {
		deadline = time.Now().Add(2 * time.Minute)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if skip, reason := shouldSkipPod(pod, opts); skip {
			result.Skipped++
			progress("skip %s/%s (%s)", pod.Namespace, pod.Name, reason)
			continue
		}
		if err := evictPod(ctx, client, pod, opts.GracePeriodSeconds, deadline, progress); err != nil {
			return result, err
		}
		result.Evicted++
	}
	progress("drain complete: %d evicted, %d skipped", result.Evicted, result.Skipped)
	return result, nil
}

func shouldSkipPod(pod *corev1.Pod, opts DrainOptions) (bool, string) {
	if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
		return true, "mirror pod"
	}
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			if opts.IgnoreDaemonSets {
				return true, "daemonset pod"
			}
			return false, ""
		}
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return true, "terminal phase"
	}
	if !opts.DeleteEmptyDirData {
		for _, vol := range pod.Spec.Volumes {
			if vol.EmptyDir != nil {
				return true, "emptyDir data (use delete-emptydir to override)"
			}
		}
	}
	return false, ""
}

// evictPod issues an Eviction, retrying on 429 (PodDisruptionBudget) until the
// deadline.
func evictPod(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, grace int64, deadline time.Time, progress func(string, ...any)) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
	}
	if grace > 0 {
		eviction.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	}
	for {
		err := client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
		switch {
		case err == nil:
			progress("evicted %s/%s", pod.Namespace, pod.Name)
			return nil
		case apierrors.IsNotFound(err):
			return nil
		case apierrors.IsTooManyRequests(err):
			if time.Now().After(deadline) {
				return fmt.Errorf("evict %s/%s: timed out waiting for PodDisruptionBudget", pod.Namespace, pod.Name)
			}
			progress("waiting on PDB for %s/%s", pod.Namespace, pod.Name)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		default:
			return fmt.Errorf("evict %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
}
