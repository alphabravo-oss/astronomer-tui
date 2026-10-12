//revive:disable:exported package-comments
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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
		{Name: "delete", Key: "Ctrl+D", Description: "Confirm deletion of selected resource", Destructive: true},
	}
	switch kind {
	case "Deployment", "StatefulSet":
		return append(common,
			Action{Name: "scale", Key: "s", Description: "Scale replicas", Destructive: true},
			Action{Name: "restart", Key: "r", Description: "Restart workload", Destructive: true},
			Action{Name: "logs", Key: "l", Description: "Open merged workload logs"},
		)
	case "DaemonSet":
		return append(common, Action{Name: "restart", Key: "r", Description: "Restart DaemonSet", Destructive: true}, Action{Name: "logs", Key: "l", Description: "Workload logs"})
	case "ReplicaSet":
		return append(common, Action{Name: "scale", Key: "s", Description: "Scale replicas", Destructive: true}, Action{Name: "logs", Key: "l", Description: "Workload logs"})
	case "CronJob":
		return append(common, Action{Name: "trigger-job", Description: "Create a Job now", Destructive: true}, Action{Name: "suspend", Description: "Suspend scheduled Jobs", Destructive: true}, Action{Name: "resume", Description: "Resume scheduling", Destructive: true})
	case "Job":
		return append(common, Action{Name: "suspend", Description: "Suspend Job", Destructive: true}, Action{Name: "resume", Description: "Resume Job", Destructive: true}, Action{Name: "logs", Key: "l", Description: "Job logs"})
	case "Node":
		return append(common, Action{Name: "cordon", Description: "Prevent new scheduling", Destructive: true}, Action{Name: "uncordon", Description: "Allow scheduling", Destructive: true}, Action{Name: "drain", Description: "Cordon and evict managed pods, respecting disruption budgets", Destructive: true})
	case "Pod":
		return append(common,
			Action{Name: "exec", Key: "x", Description: "Exec into pod", Destructive: true},
			Action{Name: "logs", Key: "l", Description: "Open pod logs"},
			Action{Name: "port-forward", Key: "f", Description: "Forward a pod port"},
		)
	case "Service":
		return append(common, Action{Name: "port-forward", Key: "f", Description: "Forward a service port"})
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
	return deleteResource(ctx, client, gvr, namespace, name, "")
}

func deleteResource(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, uid types.UID) error {
	return deleteResourceOptions(ctx, client, gvr, namespace, name, uid, false)
}
func deleteResourceOptions(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, uid types.UID, force bool) error {
	options := metav1.DeleteOptions{}
	if force {
		zero := int64(0)
		options.GracePeriodSeconds = &zero
	}
	if uid != "" {
		options.Preconditions = &metav1.Preconditions{UID: &uid}
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
	return json.Marshal(patch)
}

func diffMap(original, edited map[string]any) map[string]any {
	patch := map[string]any{}
	for key := range original {
		if _, exists := edited[key]; !exists {
			patch[key] = nil
		}
	}
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
		if !reflect.DeepEqual(originalValue, editedValue) {
			patch[key] = editedValue
		}
	}
	return patch
}
