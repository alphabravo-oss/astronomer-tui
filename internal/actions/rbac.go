//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authzv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Subject identifies an RBAC subject to resolve.
type Subject struct {
	Kind      string // User, Group, ServiceAccount
	Name      string
	Namespace string // for ServiceAccount
}

// ParseSubject parses "u:name", "g:name", or "s:namespace/sa".
func ParseSubject(spec string) (Subject, error) {
	spec = strings.TrimSpace(spec)
	switch {
	case strings.HasPrefix(spec, "u:"):
		return Subject{Kind: "User", Name: spec[2:]}, nil
	case strings.HasPrefix(spec, "g:"):
		return Subject{Kind: "Group", Name: spec[2:]}, nil
	case strings.HasPrefix(spec, "s:"):
		rest := spec[2:]
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			return Subject{}, fmt.Errorf("service account must be s:<namespace>/<name>")
		}
		return Subject{Kind: "ServiceAccount", Namespace: parts[0], Name: parts[1]}, nil
	default:
		return Subject{}, fmt.Errorf("subject must start with u:, g:, or s:")
	}
}

// AggregatedRule is a policy rule with its origin.
type AggregatedRule struct {
	Verbs     string
	APIGroups string
	Resources string
	Scope     string // ClusterRole or Role (namespace)
	Via       string // binding -> role reference
}

// ResolveSubjectRules aggregates the policy rules granted to a subject through
// ClusterRoleBindings and RoleBindings.
func ResolveSubjectRules(ctx context.Context, client kubernetes.Interface, subject Subject) ([]AggregatedRule, error) {
	var out []AggregatedRule

	crbs, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range crbs.Items {
		crb := &crbs.Items[i]
		if !subjectMatches(crb.Subjects, subject) {
			continue
		}
		rules, err := clusterRoleRules(ctx, client, crb.RoleRef.Name)
		if err != nil {
			continue
		}
		out = append(out, aggregate(rules, "cluster", "crb/"+crb.Name+"→"+crb.RoleRef.Name)...)
	}

	rbs, err := client.RbacV1().RoleBindings("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range rbs.Items {
		rb := &rbs.Items[i]
		if !subjectMatches(rb.Subjects, subject) {
			continue
		}
		var rules []rbacv1.PolicyRule
		if rb.RoleRef.Kind == "ClusterRole" {
			rules, _ = clusterRoleRules(ctx, client, rb.RoleRef.Name)
		} else {
			rules, _ = roleRules(ctx, client, rb.Namespace, rb.RoleRef.Name)
		}
		out = append(out, aggregate(rules, "ns:"+rb.Namespace, "rb/"+rb.Name+"→"+rb.RoleRef.Name)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Resources < out[j].Resources
	})
	return out, nil
}

func subjectMatches(subjects []rbacv1.Subject, want Subject) bool {
	for _, s := range subjects {
		if s.Kind != want.Kind || s.Name != want.Name {
			continue
		}
		if want.Kind == "ServiceAccount" && s.Namespace != want.Namespace {
			continue
		}
		return true
	}
	return false
}

func clusterRoleRules(ctx context.Context, client kubernetes.Interface, name string) ([]rbacv1.PolicyRule, error) {
	cr, err := client.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return cr.Rules, nil
}

func roleRules(ctx context.Context, client kubernetes.Interface, namespace, name string) ([]rbacv1.PolicyRule, error) {
	r, err := client.RbacV1().Roles(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return r.Rules, nil
}

func aggregate(rules []rbacv1.PolicyRule, scope, via string) []AggregatedRule {
	out := make([]AggregatedRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, AggregatedRule{
			Verbs:     strings.Join(r.Verbs, ","),
			APIGroups: joinOrCore(r.APIGroups),
			Resources: strings.Join(r.Resources, ","),
			Scope:     scope,
			Via:       via,
		})
	}
	return out
}

func joinOrCore(groups []string) string {
	parts := make([]string, len(groups))
	for i, g := range groups {
		if g == "" {
			parts[i] = "core"
		} else {
			parts[i] = g
		}
	}
	return strings.Join(parts, ",")
}

// CanI runs a SelfSubjectAccessReview for the current user.
func CanI(ctx context.Context, client kubernetes.Interface, verb, resource, namespace string) (bool, string, error) {
	review := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Resource:  resource,
			},
		},
	}
	result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return result.Status.Allowed, result.Status.Reason, nil
}
