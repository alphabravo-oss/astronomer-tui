package cluster

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Catalog is an immutable discovery snapshot. A failed refresh must not replace
// the last successful snapshot. Ambiguous short names require qualification.
var ErrAmbiguous = errors.New("ambiguous resource")

type Catalog struct{ Resources []Resource }
type Resource struct {
	GVR            schema.GroupVersionResource
	Kind, Singular string
	ShortNames     []string
	Namespaced     bool
}

func LoadCatalog(d discovery.DiscoveryInterface) (*Catalog, error) {
	lists, err := d.ServerPreferredResources()
	if len(lists) == 0 && err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	return CatalogFromLists(lists), err
}
func CatalogFromLists(lists []*metav1.APIResourceList) *Catalog {
	c := &Catalog{}
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") || !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "watch") {
				continue
			}
			c.Resources = append(c.Resources, Resource{GVR: gv.WithResource(r.Name), Kind: r.Kind, Singular: r.SingularName, ShortNames: r.ShortNames, Namespaced: r.Namespaced})
		}
	}
	sort.Slice(c.Resources, func(i, j int) bool { return Qualified(c.Resources[i].GVR) < Qualified(c.Resources[j].GVR) })
	return c
}
func hasVerb(verbs metav1.Verbs, verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
	}
	return false
}
func Qualified(g schema.GroupVersionResource) string {
	if g.Group == "" {
		return g.Version + "/" + g.Resource
	}
	return g.Group + "/" + g.Version + "/" + g.Resource
}
func (c *Catalog) Lookup(g schema.GroupVersionResource) (Resource, bool) {
	if c != nil {
		for _, r := range c.Resources {
			if r.GVR == g {
				return r, true
			}
		}
	}
	return Resource{}, false
}
func (c *Catalog) Resolve(query string) (schema.GroupVersionResource, error) {
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), ":"))
	var matches []Resource
	if c != nil {
		for _, r := range c.Resources {
			names := append([]string{r.GVR.Resource, r.Kind, r.Singular, Qualified(r.GVR), r.GVR.Resource + "." + r.GVR.Group}, r.ShortNames...)
			for _, n := range names {
				if n != "" && strings.ToLower(n) == q {
					matches = append(matches, r)
					break
				}
			}
		}
	}
	if len(matches) == 1 {
		return matches[0].GVR, nil
	}
	if len(matches) == 0 {
		return schema.GroupVersionResource{}, fmt.Errorf("unknown resource %q; use :refresh to reload discovery", query)
	}
	names := make([]string, len(matches))
	for i, r := range matches {
		names[i] = Qualified(r.GVR)
	}
	return schema.GroupVersionResource{}, fmt.Errorf("%w %q: use %s", ErrAmbiguous, query, strings.Join(names, " or "))
}
