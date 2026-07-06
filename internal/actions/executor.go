//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Request struct {
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
	case "delete":
		if err := DeleteResource(ctx, e.Dynamic, req.GVR, req.Namespace, req.ResourceName); err != nil {
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
	default:
		return Result{}, fmt.Errorf("unknown action %q", req.Name)
	}
}

func (e Executor) scaleWorkload(ctx context.Context, req Request, replicas int32) error {
	switch req.Kind {
	case "Deployment":
		return ScaleDeployment(ctx, e.Clientset, req.Namespace, req.ResourceName, replicas)
	case "StatefulSet":
		return ScaleStatefulSet(ctx, e.Clientset, req.Namespace, req.ResourceName, replicas)
	case "ReplicaSet":
		return ScaleReplicaSet(ctx, e.Clientset, req.Namespace, req.ResourceName, replicas)
	default:
		return fmt.Errorf("scale is not implemented for %s", req.Kind)
	}
}

func (e Executor) restartWorkload(ctx context.Context, req Request) error {
	switch req.Kind {
	case "Deployment":
		return RestartDeployment(ctx, e.Clientset, req.Namespace, req.ResourceName)
	case "StatefulSet":
		return RestartStatefulSet(ctx, e.Clientset, req.Namespace, req.ResourceName)
	case "DaemonSet":
		return RestartDaemonSet(ctx, e.Clientset, req.Namespace, req.ResourceName)
	default:
		return fmt.Errorf("restart is not implemented for %s", req.Kind)
	}
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
	default:
		return safety.Verb(action)
	}
}
