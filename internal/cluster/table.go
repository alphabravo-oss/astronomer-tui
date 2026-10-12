package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"net/http"
	"net/url"
	"strings"
)

// FetchTable delegates printer logic to the server, including aggregated APIs
// whose CRD definitions the current user cannot read.
func FetchTable(ctx context.Context, cfg *rest.Config, g schema.GroupVersionResource, namespace string) (*metav1.Table, error) {
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	path := "/api/" + url.PathEscape(g.Version)
	if g.Group != "" {
		path = "/apis/" + url.PathEscape(g.Group) + "/" + url.PathEscape(g.Version)
	}
	if namespace != "" {
		path += "/namespaces/" + url.PathEscape(namespace)
	}
	path += "/" + url.PathEscape(g.Resource)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+path+"?includeObject=Metadata", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json;as=Table;g=meta.k8s.io;v=v1,application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server table: %s", resp.Status)
	}
	var table metav1.Table
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&table); err != nil {
		return nil, err
	}
	if table.Kind != "Table" {
		return nil, fmt.Errorf("server does not provide Table responses")
	}
	return &table, nil
}
