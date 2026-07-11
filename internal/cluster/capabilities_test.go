package cluster

import (
	"context"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestCanIUsesSelfSubjectAccessReview(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	clientset.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		review := create.GetObject().(*authorizationv1.SelfSubjectAccessReview)
		if review.Spec.ResourceAttributes.Verb != "delete" || review.Spec.ResourceAttributes.Resource != "pods" || review.Spec.ResourceAttributes.Name != "api" {
			t.Fatalf("unexpected review %#v", review.Spec.ResourceAttributes)
		}
		return true, &authorizationv1.SelfSubjectAccessReview{
			ObjectMeta: metav1.ObjectMeta{Name: "result"},
			Status:     authorizationv1.SubjectAccessReviewStatus{Allowed: true, Reason: "test policy"},
		}, nil
	})
	allowed, reason, err := CanI(context.Background(), &Client{Clientset: clientset}, AccessRequest{
		Verb: "delete", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespace: "default", Name: "api",
	})
	if err != nil || !allowed || reason != "test policy" {
		t.Fatalf("allowed=%v reason=%q err=%v", allowed, reason, err)
	}
}

func TestResourceInfoSupportsVerb(t *testing.T) {
	info := ResourceInfo{Verbs: map[string]struct{}{"get": {}, "list": {}}}
	if !info.Supports("GET") || info.Supports("delete") {
		t.Fatalf("unexpected verb support")
	}
}
