package ui

import (
	"context"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	"time"
)

func (a *App) watchMetrics(ctx context.Context) {
	client, gvr, generation := a.options.Client, a.currentGVR, a.generation
	if client == nil || client.Dynamic == nil || client.Discovery == nil || gvr.Group != "" || (gvr.Resource != "pods" && gvr.Resource != "nodes") {
		return
	}
	namespace := a.resourceNamespace(gvr)
	base := a.renderer
	a.renderer = views.MetricsRenderer{Renderer: base}
	go func() {
		for {
			work, cancel := context.WithTimeout(ctx, 5*time.Second)
			usage, err := cluster.FetchMetrics(work, client.Dynamic, gvr.Resource, namespace)
			cancel()
			a.queueUpdate(ctx, func() {
				if a.generation != generation {
					return
				}
				a.metricsError = err
				a.renderer = views.MetricsRenderer{Renderer: base, Usage: usage, Error: err, Now: time.Now()}
				a.renderCache = nil
				a.renderCurrent()
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
}
