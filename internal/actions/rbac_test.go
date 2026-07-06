package actions

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestParseSubject(t *testing.T) {
	cases := map[string]Subject{
		"u:alice":               {Kind: "User", Name: "alice"},
		"g:devs":                {Kind: "Group", Name: "devs"},
		"s:kube-system/default": {Kind: "ServiceAccount", Namespace: "kube-system", Name: "default"},
	}
	for spec, want := range cases {
		got, err := ParseSubject(spec)
		if err != nil || got != want {
			t.Fatalf("ParseSubject(%q) = %#v, %v; want %#v", spec, got, err, want)
		}
	}
	if _, err := ParseSubject("bogus"); err == nil {
		t.Fatal("expected error for bad subject")
	}
	if _, err := ParseSubject("s:noslash"); err == nil {
		t.Fatal("expected error for SA without namespace")
	}
}

func TestResolveSubjectRules(t *testing.T) {
	client := kubefake.NewSimpleClientset(
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "admin"},
			Rules:      []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "alice-admin"},
			Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice"}},
			RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "admin"},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "team"},
			Rules:      []rbacv1.PolicyRule{{Verbs: []string{"get"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}}},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "alice-reader", Namespace: "team"},
			Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice"}},
			RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "reader"},
		},
		// Binding for someone else must be ignored.
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "bob-admin"},
			Subjects:   []rbacv1.Subject{{Kind: "User", Name: "bob"}},
			RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "admin"},
		},
	)
	rules, err := ResolveSubjectRules(context.Background(), client, Subject{Kind: "User", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 aggregated rules for alice, got %d: %#v", len(rules), rules)
	}
	var sawCluster, sawNs bool
	for _, r := range rules {
		if r.Scope == "cluster" && r.Resources == "pods" && r.APIGroups == "core" {
			sawCluster = true
		}
		if r.Scope == "ns:team" && r.Resources == "deployments" {
			sawNs = true
		}
	}
	if !sawCluster || !sawNs {
		t.Fatalf("missing expected rules: cluster=%v ns=%v (%#v)", sawCluster, sawNs, rules)
	}
}
