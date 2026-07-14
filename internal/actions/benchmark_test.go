package actions

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type benchmarkDoer func(*http.Request) (*http.Response, error)

func (f benchmarkDoer) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestRunHTTPBenchmarkCollectsBoundedResults(t *testing.T) {
	spec := BenchmarkSpec{PortForwardID: "pf-1", URL: "http://127.0.0.1:8080/health", Requests: 20, Concurrency: 4, Timeout: time.Second}
	result := RunHTTPBenchmark(context.Background(), benchmarkDoer(func(request *http.Request) (*http.Response, error) {
		if request.UserAgent() != "astrotui-benchmark/1" {
			t.Fatalf("unexpected user agent %q", request.UserAgent())
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), spec)
	if result.Error != "" || result.Completed != 20 || result.Failed != 0 || result.StatusCodes[http.StatusNoContent] != 20 {
		t.Fatalf("unexpected benchmark result %#v", result)
	}
	if result.Max < result.Min || result.P95 < result.Median {
		t.Fatalf("invalid latency ordering %#v", result)
	}
}

func TestBenchmarkRejectsExternalAndExcessiveTargets(t *testing.T) {
	base := BenchmarkSpec{URL: "http://127.0.0.1:8080/", Requests: 10, Concurrency: 2, Timeout: time.Second}
	for name, mutate := range map[string]func(*BenchmarkSpec){
		"external":    func(spec *BenchmarkSpec) { spec.URL = "https://example.com/" },
		"hostname":    func(spec *BenchmarkSpec) { spec.URL = "http://localhost:8080/" },
		"credentials": func(spec *BenchmarkSpec) { spec.URL = "http://user:pass@127.0.0.1/" },
		"requests":    func(spec *BenchmarkSpec) { spec.Requests = MaxBenchmarkRequests + 1 },
		"concurrency": func(spec *BenchmarkSpec) { spec.Concurrency = MaxBenchmarkConcurrency + 1 },
		"timeout":     func(spec *BenchmarkSpec) { spec.Timeout = MaxBenchmarkDuration + time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			spec := base
			mutate(&spec)
			if err := ValidateBenchmarkSpec(spec); err == nil {
				t.Fatalf("expected invalid spec %#v", spec)
			}
		})
	}
}

func TestRunHTTPBenchmarkDoesNotFollowRedirects(t *testing.T) {
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer source.Close()
	result := RunHTTPBenchmark(context.Background(), nil, BenchmarkSpec{URL: source.URL, Requests: 1, Concurrency: 1, Timeout: time.Second})
	if result.Error != "" || result.StatusCodes[http.StatusFound] != 1 || redirected != 0 {
		t.Fatalf("benchmark followed redirect: result=%#v redirected=%d", result, redirected)
	}
}

func TestBenchmarkManagerTracksAndCancelsRuns(t *testing.T) {
	manager := NewBenchmarkManager()
	started := make(chan struct{})
	finished := make(chan struct{})
	spec := BenchmarkSpec{URL: "http://127.0.0.1:8080/", Requests: 1, Concurrency: 1, Timeout: time.Second}
	id, err := manager.Start(context.Background(), spec, func(ctx context.Context, _ BenchmarkSpec) BenchmarkResult {
		close(started)
		<-ctx.Done()
		close(finished)
		return BenchmarkResult{Error: ctx.Err().Error()}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !manager.Cancel(id) {
		t.Fatal("expected running benchmark cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("benchmark did not observe cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runs := manager.List()
		if len(runs) == 1 && runs[0].Status == "canceled" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("benchmark was not marked canceled: %#v", manager.List())
}
