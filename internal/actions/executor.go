//revive:disable:exported package-comments
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Target struct {
	Context         string
	GVR             schema.GroupVersionResource
	Kind            string
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

type Request struct {
	Target       Target
	Name         string
	Kind         string
	Namespace    string
	ResourceName string
	GVR          schema.GroupVersionResource
	Args         map[string]string
	ConfirmText  string
}

type Result struct {
	Message string
}

type Preview struct {
	Summary string
	Patch   string
}

type Executor struct {
	Clientset      kubernetes.Interface
	Dynamic        dynamic.Interface
	Config         config.Config
	CurrentContext string
	RequireTarget  bool
}

func (e Executor) Run(ctx context.Context, req Request) (Result, error) {
	req = req.normalized()
	verb := safetyVerb(req.Name)
	if err := safety.Authorize(e.Config, e.CurrentContext, verb, req.ConfirmText); err != nil {
		return Result{}, err
	}
	if err := e.validateTarget(ctx, req); err != nil {
		return Result{}, err
	}
	switch req.Name {
	case "delete":
		var err error
		if req.Target.GVR.Resource != "" {
			err = DeleteResourceBound(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, req.Target.UID, req.Target.ResourceVersion)
		} else {
			err = DeleteResource(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName)
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("deleted %s/%s", req.Kind, req.ResourceName)}, nil
	case "scale":
		replicas, err := strconv.ParseInt(req.Args["replicas"], 10, 32)
		if err != nil {
			return Result{}, fmt.Errorf("replicas must be an integer: %w", err)
		}
		if err := e.scaleWorkload(ctx, req, int32(replicas)); err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("scaled %s/%s to %d", req.Kind, req.ResourceName, replicas)}, nil
	case "restart":
		if err := e.restartWorkload(ctx, req); err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("restarted %s/%s", req.Kind, req.ResourceName)}, nil
	case "suspend", "resume":
		suspend := req.Name == "suspend"
		if err := e.patchSuspend(ctx, req, suspend); err != nil {
			return Result{}, err
		}
		pastTense := "resumed"
		if suspend {
			pastTense = "suspended"
		}
		return Result{Message: fmt.Sprintf("%s %s/%s", pastTense, req.Kind, req.ResourceName)}, nil
	default:
		return Result{}, fmt.Errorf("unknown action %q", req.Name)
	}
}

func (e Executor) Preview(ctx context.Context, req Request) (Preview, error) {
	req = req.normalized()
	if err := e.validateTarget(ctx, req); err != nil {
		return Preview{}, err
	}
	switch req.Name {
	case "delete":
		if err := DeleteResourceDryRun(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, req.Target.UID, req.Target.ResourceVersion); err != nil {
			return Preview{}, err
		}
		return Preview{Summary: fmt.Sprintf("Dry-run: delete %s/%s in namespace %s", req.Kind, req.ResourceName, req.Namespace)}, nil
	case "scale":
		replicas, err := strconv.ParseInt(req.Args["replicas"], 10, 32)
		if err != nil {
			return Preview{}, fmt.Errorf("replicas must be an integer: %w", err)
		}
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"replicas": replicas}})
		if err := PatchResourceDryRun(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch); err != nil {
			return Preview{}, err
		}
		return Preview{Summary: fmt.Sprintf("Dry-run: scale %s/%s to %d", req.Kind, req.ResourceName, replicas), Patch: string(patch)}, nil
	case "restart":
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339)}}}}})
		if err := PatchResourceDryRun(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch); err != nil {
			return Preview{}, err
		}
		return Preview{Summary: fmt.Sprintf("Dry-run: restart %s/%s", req.Kind, req.ResourceName), Patch: string(patch)}, nil
	case "suspend", "resume":
		suspend := req.Name == "suspend"
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"suspend": suspend}})
		if err := PatchResourceDryRun(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch); err != nil {
			return Preview{}, err
		}
		return Preview{Summary: fmt.Sprintf("Dry-run: %s %s/%s", req.Name, req.Kind, req.ResourceName), Patch: string(patch)}, nil
	default:
		return Preview{}, fmt.Errorf("preview is not implemented for %q", req.Name)
	}
}

func (r Request) normalized() Request {
	if r.Target.GVR.Resource == "" {
		return r
	}
	r.GVR = r.Target.GVR
	r.Kind = r.Target.Kind
	r.Namespace = r.Target.Namespace
	r.ResourceName = r.Target.Name
	return r
}

func (e Executor) validateTarget(ctx context.Context, req Request) error {
	target := req.Target
	if target.GVR.Resource == "" {
		if e.RequireTarget {
			return fmt.Errorf("action target is not bound")
		}
		return nil
	}
	if target.Context == "" || target.Context != e.CurrentContext {
		return fmt.Errorf("action target context changed from %q to %q", target.Context, e.CurrentContext)
	}
	if e.Dynamic == nil {
		return fmt.Errorf("dynamic client is required to validate action target")
	}
	resource := e.Dynamic.Resource(target.GVR)
	var currentUID types.UID
	var currentVersion string
	var err error
	if target.Namespace != "" {
		obj, getErr := resource.Namespace(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		err = getErr
		if obj != nil {
			currentUID, currentVersion = obj.GetUID(), obj.GetResourceVersion()
		}
	} else {
		obj, getErr := resource.Get(ctx, target.Name, metav1.GetOptions{})
		err = getErr
		if obj != nil {
			currentUID, currentVersion = obj.GetUID(), obj.GetResourceVersion()
		}
	}
	if err != nil {
		return fmt.Errorf("validate action target: %w", err)
	}
	if target.UID == "" || target.ResourceVersion == "" {
		return fmt.Errorf("action target is missing UID or resourceVersion")
	}
	if currentUID != target.UID || currentVersion != target.ResourceVersion {
		return fmt.Errorf("action target changed: expected uid=%s resourceVersion=%s, got uid=%s resourceVersion=%s", target.UID, target.ResourceVersion, currentUID, currentVersion)
	}
	return nil
}

func (e Executor) scaleWorkload(ctx context.Context, req Request, replicas int32) error {
	if req.Target.GVR.Resource != "" {
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"replicas": replicas}})
		_, err := PatchResource(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch)
		return err
	}
	switch req.Kind {
	case "Deployment":
		return ScaleDeployment(ctx, e.Clientset, req.Namespace, req.ResourceName, replicas)
	case "StatefulSet":
		return ScaleStatefulSet(ctx, e.Clientset, req.Namespace, req.ResourceName, replicas)
	default:
		return fmt.Errorf("scale is not implemented for %s", req.Kind)
	}
}

func (e Executor) restartWorkload(ctx context.Context, req Request) error {
	if req.Target.GVR.Resource != "" {
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339)}}}}})
		_, err := PatchResource(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch)
		return err
	}
	switch req.Kind {
	case "Deployment":
		return RestartDeployment(ctx, e.Clientset, req.Namespace, req.ResourceName)
	case "StatefulSet":
		return RestartStatefulSet(ctx, e.Clientset, req.Namespace, req.ResourceName)
	default:
		return fmt.Errorf("restart is not implemented for %s", req.Kind)
	}
}

func (e Executor) patchSuspend(ctx context.Context, req Request, suspend bool) error {
	if req.Kind != "Job" && req.Kind != "CronJob" {
		return fmt.Errorf("%s is not implemented for %s", req.Name, req.Kind)
	}
	patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": req.Target.ResourceVersion}, "spec": map[string]any{"suspend": suspend}})
	_, err := PatchResource(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, patch)
	return err
}

func safetyVerb(action string) safety.Verb {
	switch action {
	case "delete":
		return safety.Delete
	case "scale":
		return safety.Scale
	case "restart":
		return safety.Restart
	case "edit":
		return safety.Edit
	case "suspend", "resume":
		return safety.Patch
	default:
		return safety.Verb(action)
	}
}
