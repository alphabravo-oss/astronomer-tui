//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Request struct {
	Name         string
	Kind         string
	Namespace    string
	ResourceName string
	UID          types.UID
	GVR          schema.GroupVersionResource
	Args         map[string]string
	ConfirmText  string
}

type Result struct {
	Message string
}

type Executor struct {
	Clientset      kubernetes.Interface
	Dynamic        dynamic.Interface
	Config         config.Config
	CurrentContext string
}

func (e Executor) Run(ctx context.Context, req Request) (Result, error) {
	verb := safetyVerb(req.Name)
	if err := safety.Authorize(e.Config, e.CurrentContext, verb, req.ConfirmText); err != nil {
		return Result{}, err
	}
	switch req.Name {
	case "delete", "force-delete":
		if err := deleteResourceOptions(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName, req.UID, req.Name == "force-delete"); err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("deleted %s/%s", req.Kind, req.ResourceName)}, nil
	case "scale":
		replicas, err := strconv.ParseInt(req.Args["replicas"], 10, 32)
		if err != nil {
			return Result{}, fmt.Errorf("replicas must be an integer: %w", err)
		}
		if replicas < 0 {
			return Result{}, fmt.Errorf("replicas must be non-negative")
		}
		if err := e.scaleWorkload(ctx, req, int32(replicas)); err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("scaled %s/%s to %d", req.Kind, req.ResourceName, replicas)}, nil
	case "cordon", "uncordon", "suspend", "resume", "trigger-job", "drain":
		var err error
		message := req.Name + " completed for " + req.Kind + "/" + req.ResourceName
		switch req.Name {
		case "trigger-job":
			if req.Kind != "CronJob" {
				return Result{}, fmt.Errorf("trigger requires CronJob")
			}
			message, err = e.triggerJob(ctx, req)
		case "drain":
			if req.Kind != "Node" {
				return Result{}, fmt.Errorf("drain requires Node")
			}
			err = e.drain(ctx, req)
		case "cordon", "uncordon":
			if req.Kind != "Node" {
				return Result{}, fmt.Errorf("cordon requires Node")
			}
			err = e.mutate(ctx, req, map[string]any{"spec": map[string]any{"unschedulable": req.Name == "cordon"}})
		case "suspend", "resume":
			if req.Kind != "CronJob" && req.Kind != "Job" {
				return Result{}, fmt.Errorf("suspend requires Job or CronJob")
			}
			err = e.mutate(ctx, req, map[string]any{"spec": map[string]any{"suspend": req.Name == "suspend"}})
		}
		return Result{Message: message}, err
	case "restart":
		if err := e.restartWorkload(ctx, req); err != nil {
			return Result{}, err
		}
		return Result{Message: fmt.Sprintf("restarted %s/%s", req.Kind, req.ResourceName)}, nil
	default:
		return Result{}, fmt.Errorf("unknown action %q", req.Name)
	}
}

func (e Executor) scaleWorkload(ctx context.Context, req Request, replicas int32) error {
	if req.Kind == "Deployment" || req.Kind == "StatefulSet" || req.Kind == "ReplicaSet" {
		if req.UID != "" && e.Dynamic != nil || req.Kind == "ReplicaSet" {
			return e.mutate(ctx, req, map[string]any{"spec": map[string]any{"replicas": replicas}})
		}
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
	if req.Kind == "Deployment" || req.Kind == "StatefulSet" || req.Kind == "DaemonSet" {
		if req.UID != "" && e.Dynamic != nil || req.Kind == "DaemonSet" {
			return e.mutate(ctx, req, map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339Nano)}}}}})
		}
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

func safetyVerb(action string) safety.Verb {
	switch action {
	case "delete", "force-delete":
		return safety.Delete
	case "scale":
		return safety.Scale
	case "restart":
		return safety.Restart
	case "edit":
		return safety.Edit
	case "cordon", "uncordon", "drain", "trigger-job", "suspend", "resume":
		return safety.Patch
	default:
		return safety.Verb(action)
	}
}
