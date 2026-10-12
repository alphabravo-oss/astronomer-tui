//go:build e2e

package ui

import (
	"context"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"os"
	"testing"
	"time"
)

func TestLiveMetricsExtension(t *testing.T) {
	if os.Getenv("ASTROTUI_METRICS_E2E") != "1" {
		t.Skip("requires isolated metrics-server fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := cluster.NewClient(ctx, cluster.ClientOptions{Context: os.Getenv("ASTROTUI_E2E_CONTEXT"), Namespace: "kube-system"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"nodes", "pods"} {
		usage, err := cluster.FetchMetrics(ctx, client.Dynamic, kind, "kube-system")
		if err != nil {
			t.Fatal(err)
		}
		if len(usage) == 0 {
			t.Fatal("empty metrics", kind)
		}
		for _, u := range usage {
			if u.Timestamp.IsZero() || u.MemoryBytes <= 0 {
				t.Fatal("invalid live usage", u)
			}
		}
	}
}
