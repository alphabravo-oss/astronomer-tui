package cluster

import (
	"context"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
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

func TestReviewIdentityUsesSelfSubjectReview(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	clientset.PrependReactor("create", "selfsubjectreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		if _, ok := action.(ktesting.CreateAction).GetObject().(*authenticationv1.SelfSubjectReview); !ok {
			t.Fatalf("unexpected review object %T", action.(ktesting.CreateAction).GetObject())
		}
		return true, &authenticationv1.SelfSubjectReview{Status: authenticationv1.SelfSubjectReviewStatus{UserInfo: authenticationv1.UserInfo{
			Username: "alice", UID: "1001", Groups: []string{"operators"}, Extra: map[string]authenticationv1.ExtraValue{"scopes": {"dev"}},
		}}}, nil
	})
	identity, err := ReviewIdentity(context.Background(), &Client{Clientset: clientset})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Username != "alice" || identity.UID != "1001" || len(identity.Groups) != 1 || identity.Extra["scopes"][0] != "dev" {
		t.Fatalf("unexpected identity %#v", identity)
	}
}
