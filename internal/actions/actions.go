//revive:disable:exported package-comments
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

type Action struct {
	Name        string
	Key         string
	Description string
	Destructive bool
}

func CatalogFor(kind string) []Action {
	common := []Action{
		{Name: "describe", Key: "enter", Description: "Open resource details"},
		{Name: "edit", Key: "e", Description: "Edit YAML in $EDITOR", Destructive: true},
		{Name: "delete", Key: "d", Description: "Delete selected resource", Destructive: true},
	}
	switch kind {
	case "Deployment", "StatefulSet":
		return append(common,
			Action{Name: "scale", Key: "s", Description: "Scale replicas", Destructive: true},
			Action{Name: "restart", Key: "r", Description: "Restart workload", Destructive: true},
			Action{Name: "logs", Key: "l", Description: "Open merged workload logs"},
		)
	case "Pod":
		return append(common,
			Action{Name: "exec", Key: "x", Description: "Exec into pod", Destructive: true},
			Action{Name: "logs", Key: "l", Description: "Open pod logs"},
			Action{Name: "port-forward", Key: "f", Description: "Forward a pod port", Destructive: true},
		)
	case "Service":
		return append(common, Action{Name: "port-forward", Key: "f", Description: "Forward a service port", Destructive: true})
	default:
		return common
	}
}

func RestartDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, time.Now().Format(time.RFC3339))
	_, err := client.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

func RestartStatefulSet(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, time.Now().Format(time.RFC3339))
	_, err := client.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

func ScaleDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string, replicas int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		deployment.Spec.Replicas = &replicas
		_, err = client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		return err
	})
}

func ScaleStatefulSet(ctx context.Context, client kubernetes.Interface, namespace, name string, replicas int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		statefulSet, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		statefulSet.Spec.Replicas = &replicas
		_, err = client.AppsV1().StatefulSets(namespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
		return err
	})
}

func DeploymentRestartAnnotation(d appsv1.Deployment) string {
	return d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
}

func StatefulSetRestartAnnotation(s appsv1.StatefulSet) string {
	return s.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
}

func DeleteResource(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) error {
	return deleteResource(ctx, client, gvr, namespace, name, metav1.DeleteOptions{})
}

func DeleteResourceBound(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, uid types.UID, resourceVersion string) error {
	options := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}}
	return deleteResource(ctx, client, gvr, namespace, name, options)
}

func deleteResource(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, options metav1.DeleteOptions) error {
	resource := client.Resource(gvr)
	if namespace != "" {
		return resource.Namespace(namespace).Delete(ctx, name, options)
	}
	return resource.Delete(ctx, name, options)
}

func DeleteResourceDryRun(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, uid types.UID, resourceVersion string) error {
	options := metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}}
	if uid != "" || resourceVersion != "" {
		options.Preconditions = &metav1.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}
	}
	resource := client.Resource(gvr)
	if namespace != "" {
		return resource.Namespace(namespace).Delete(ctx, name, options)
	}
	return resource.Delete(ctx, name, options)
}

func YAMLForEdit(obj *unstructured.Unstructured) ([]byte, error) {
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(data)
}

func StrategicMergePatch(original, edited []byte) ([]byte, error) {
	originalJSON, err := yaml.YAMLToJSON(original)
	if err != nil {
		return nil, err
	}
	editedJSON, err := yaml.YAMLToJSON(edited)
	if err != nil {
		return nil, err
	}
	patch, err := strategicMergePatch(originalJSON, editedJSON)
	if err != nil {
		return nil, err
	}
	if string(patch) == "{}" {
		return nil, nil
	}
	return patch, nil
}

func PatchResource(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, patch []byte) (*unstructured.Unstructured, error) {
	resource := client.Resource(gvr)
	if namespace != "" {
		return resource.Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return resource.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
}

func PatchResourceDryRun(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, patch []byte) error {
	resource := client.Resource(gvr)
	options := metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}
	if namespace != "" {
		_, err := resource.Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, options)
		return err
	}
	_, err := resource.Patch(ctx, name, types.MergePatchType, patch, options)
	return err
}

func strategicMergePatch(originalJSON, editedJSON []byte) ([]byte, error) {
	var original map[string]any
	var edited map[string]any
	if err := json.Unmarshal(originalJSON, &original); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(editedJSON, &edited); err != nil {
		return nil, err
	}
	patch := diffMap(original, edited)
	if len(patch) > 0 {
		if metadata, ok := original["metadata"].(map[string]any); ok {
			if resourceVersion, ok := metadata["resourceVersion"].(string); ok && resourceVersion != "" {
				patchMetadata, _ := patch["metadata"].(map[string]any)
				if patchMetadata == nil {
					patchMetadata = map[string]any{}
				}
				patchMetadata["resourceVersion"] = resourceVersion
				patch["metadata"] = patchMetadata
			}
		}
	}
	return json.Marshal(patch)
}

func diffMap(original, edited map[string]any) map[string]any {
	patch := map[string]any{}
	for key, editedValue := range edited {
		originalValue, ok := original[key]
		if !ok {
			patch[key] = editedValue
			continue
		}
		editedMap, editedIsMap := editedValue.(map[string]any)
		originalMap, originalIsMap := originalValue.(map[string]any)
		if editedIsMap && originalIsMap {
			nested := diffMap(originalMap, editedMap)
			if len(nested) > 0 {
				patch[key] = nested
			}
			continue
		}
		if fmt.Sprint(originalValue) != fmt.Sprint(editedValue) {
			patch[key] = editedValue
		}
	}
	return patch
}
