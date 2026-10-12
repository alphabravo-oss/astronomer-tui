package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Grants describe RBAC bindings, not decisions by every configured authorizer.
type Grant struct {
	Subject       rbacv1.Subject
	Namespace     string // Empty means cluster-wide.
	Binding, Role string
	Rule          rbacv1.PolicyRule
}
type RBACSnapshot struct {
	Grants   []Grant
	Warnings []string
}

func ReadRBAC(ctx context.Context, client kubernetes.Interface, namespace string) RBACSnapshot {
	var out RBACSnapshot
	roles := map[string][]rbacv1.PolicyRule{}
	cr, err := client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Warnings = append(out.Warnings, "ClusterRoles: "+err.Error())
	} else {
		for _, r := range cr.Items {
			roles["ClusterRole/"+r.Name] = r.Rules
		}
	}
	rr, err := client.RbacV1().Roles(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Warnings = append(out.Warnings, "Roles: "+err.Error())
	} else {
		for _, r := range rr.Items {
			roles["Role/"+r.Namespace+"/"+r.Name] = r.Rules
		}
	}
	add := func(ns, binding string, ref rbacv1.RoleRef, subjects []rbacv1.Subject) {
		key := ref.Kind + "/" + ref.Name
		if ref.Kind == "Role" {
			key = ref.Kind + "/" + ns + "/" + ref.Name
		}
		rules, ok := roles[key]
		if !ok {
			out.Warnings = append(out.Warnings, binding+": unresolved "+key)
			return
		}
		for _, subject := range subjects {
			if subject.Kind == "ServiceAccount" && subject.Namespace == "" {
				subject.Namespace = ns
			}
			for _, rule := range rules {
				out.Grants = append(out.Grants, Grant{subject, ns, binding, key, rule})
			}
		}
	}
	cb, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Warnings = append(out.Warnings, "ClusterRoleBindings: "+err.Error())
	} else {
		for _, b := range cb.Items {
			add("", "ClusterRoleBinding/"+b.Name, b.RoleRef, b.Subjects)
		}
	}
	rb, err := client.RbacV1().RoleBindings(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Warnings = append(out.Warnings, "RoleBindings: "+err.Error())
	} else {
		for _, b := range rb.Items {
			add(b.Namespace, "RoleBinding/"+b.Namespace+"/"+b.Name, b.RoleRef, b.Subjects)
		}
	}
	sort.SliceStable(out.Grants, func(i, j int) bool {
		a, b := out.Grants[i], out.Grants[j]
		return SubjectName(a.Subject)+a.Binding < SubjectName(b.Subject)+b.Binding
	})
	return out
}
func SubjectName(s rbacv1.Subject) string {
	if s.Kind == "ServiceAccount" {
		return s.Kind + "/" + s.Namespace + "/" + s.Name
	}
	return s.Kind + "/" + s.Name
}
func (s RBACSnapshot) Subjects(kind string) []rbacv1.Subject {
	seen := map[string]bool{}
	var out []rbacv1.Subject
	for _, g := range s.Grants {
		key := SubjectName(g.Subject)
		if !seen[key] && (kind == "" || kind == g.Subject.Kind) {
			seen[key] = true
			out = append(out, g.Subject)
		}
	}
	return out
}
func (s RBACSnapshot) ForSubject(subject rbacv1.Subject, groups []string) []Grant {
	var out []Grant
	if subject.Kind == "User" {
		group := "system:authenticated"
		if subject.Name == "system:anonymous" {
			group = "system:unauthenticated"
		}
		groups = append(append([]string{}, groups...), group)
	}
	if subject.Kind == "ServiceAccount" {
		groups = append(append([]string{}, groups...), "system:serviceaccounts", "system:serviceaccounts:"+subject.Namespace, "system:authenticated")
	}
	for _, g := range s.Grants {
		match := SubjectName(g.Subject) == SubjectName(subject)
		if g.Subject.Kind == "Group" {
			for _, group := range groups {
				if g.Subject.Name == group {
					match = true
				}
			}
		}
		if match {
			out = append(out, g)
		}
	}
	return out
}

// Allows checks resource requests only. A RoleBinding never grants cluster scope.
// An empty name asks for unrestricted access, not a resourceNames-restricted grant.
func (g Grant) Allows(verb, group, resource, namespace, name string) bool {
	if g.Namespace != "" && (namespace == "" || g.Namespace != namespace) {
		return false
	}
	matches := func(values []string, value string) bool {
		for _, v := range values {
			if v == "*" || v == value {
				return true
			}
		}
		return false
	}
	if !matches(g.Rule.Verbs, verb) || !matches(g.Rule.APIGroups, group) {
		return false
	}
	resourceMatch := matches(g.Rule.Resources, resource)
	if _, sub, ok := strings.Cut(resource, "/"); ok {
		for _, r := range g.Rule.Resources {
			if r == "*/"+sub {
				resourceMatch = true
			}
		}
	}
	if !resourceMatch {
		return false
	}
	if len(g.Rule.ResourceNames) > 0 {
		if name == "" {
			return false
		}
		for _, n := range g.Rule.ResourceNames {
			if n == name {
				return true
			}
		}
		return false
	}
	return true
}
func (s RBACSnapshot) WhoCan(verb, group, resource, namespace, name string) []Grant {
	var out []Grant
	for _, g := range s.Grants {
		if g.Allows(verb, group, resource, namespace, name) {
			out = append(out, g)
		}
	}
	return out
}
func FormatGrants(grants []Grant, warnings []string) string {
	var b strings.Builder
	b.WriteString("RBAC binding evidence only; other authorizers and unknown user groups may change effective access.\n")
	for _, warning := range warnings {
		fmt.Fprintf(&b, "INCOMPLETE: %s\n", warning)
	}
	if len(grants) == 0 {
		b.WriteString("\nNo matching visible grants. This is not proof of denial.\n")
	}
	for _, g := range grants {
		scope := g.Namespace
		if scope == "" {
			scope = "cluster-wide"
		}
		fmt.Fprintf(&b, "\n%s · %s\n  %s -> %s\n  verbs=%s groups=%q resources=%s names=%v", SubjectName(g.Subject), scope, g.Binding, g.Role, strings.Join(g.Rule.Verbs, ","), g.Rule.APIGroups, strings.Join(g.Rule.Resources, ","), g.Rule.ResourceNames)
		if len(g.Rule.NonResourceURLs) > 0 && g.Namespace == "" {
			fmt.Fprintf(&b, " urls=%v", g.Rule.NonResourceURLs)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
