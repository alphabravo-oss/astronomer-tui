//revive:disable:exported package-comments
package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceInfo struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
	Verbs      map[string]struct{}
	ShortNames []string
	Categories []string
}

func (r ResourceInfo) Supports(verb string) bool {
	_, ok := r.Verbs[strings.ToLower(verb)]
	return ok
}

func DiscoverResourceInfo(client *Client) ([]ResourceInfo, error) {
	if client == nil || client.Discovery == nil {
		return nil, fmt.Errorf("discovery client is required")
	}
	lists, err := client.Discovery.ServerPreferredResources()
	if err != nil && len(lists) == 0 {
		return nil, fmt.Errorf("discover resources: %w", err)
	}
	var out []ResourceInfo
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if len(resource.Verbs) == 0 || containsSlash(resource.Name) {
				continue
			}
			verbs := make(map[string]struct{}, len(resource.Verbs))
			for _, verb := range resource.Verbs {
				verbs[strings.ToLower(verb)] = struct{}{}
			}
			out = append(out, ResourceInfo{
				GVR:        gv.WithResource(resource.Name),
				Kind:       resource.Kind,
				Namespaced: resource.Namespaced,
				Verbs:      verbs,
				ShortNames: append([]string(nil), resource.ShortNames...),
				Categories: append([]string(nil), resource.Categories...),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GVR.String() < out[j].GVR.String() })
	return out, err
}

func ResourceInfoIndex(resources []ResourceInfo) map[schema.GroupVersionResource]ResourceInfo {
	index := make(map[schema.GroupVersionResource]ResourceInfo, len(resources))
	for _, resource := range resources {
		index[resource.GVR] = resource
	}
	return index
}

type AccessRequest struct {
	Verb        string
	GVR         schema.GroupVersionResource
	Namespace   string
	Name        string
	Subresource string
}

func CanI(ctx context.Context, client *Client, request AccessRequest) (bool, string, error) {
	if client == nil || client.Clientset == nil {
		return false, "", fmt.Errorf("kubernetes clientset is required")
	}
	review, err := client.Clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
			Namespace:   request.Namespace,
			Verb:        request.Verb,
			Group:       request.GVR.Group,
			Version:     request.GVR.Version,
			Resource:    request.GVR.Resource,
			Subresource: request.Subresource,
			Name:        request.Name,
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return review.Status.Allowed, review.Status.Reason, nil
}
