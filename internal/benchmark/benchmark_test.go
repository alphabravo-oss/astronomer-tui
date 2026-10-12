package benchmark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBudgetStatusAndPersistence(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.Header.Get("X-Test") != "yes" {
			t.Error("request config missing")
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	r, err := Run(context.Background(), Spec{URL: server.URL + "/?token=private", Requests: 20, Concurrency: 3, Method: "POST", Body: "data", Headers: map[string]string{"X-Test": "yes"}})
	if err != nil || calls.Load() != 20 || r.Completed != 20 || r.Failed != 20 || r.StatusCodes[503] != 20 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.P95MS < r.P50MS || r.MaxMS < r.P99MS {
		t.Fatal("invalid latency distribution")
	}
	file, err := Save(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	stat, _ := os.Stat(file)
	if strings.Contains(string(data), "private") || stat.Mode().Perm() != 0600 {
		t.Fatal("sensitive or public report")
	}
}
func TestCancelAndNoRedirect(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	r, err := Run(context.Background(), Spec{URL: redirect.URL, Requests: 2, Concurrency: 1, Method: "GET"})
	if err != nil || r.StatusCodes[302] != 2 || reached.Load() != 0 {
		t.Fatal("followed redirect")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err = Run(ctx, Spec{URL: slow.URL, Requests: 1000, Concurrency: 2, Method: "GET"})
	if err != nil || !r.Cancelled || r.Completed > 2 || time.Since(start) > time.Second {
		t.Fatalf("cancellation failed: %+v %v", r, err)
	}
}
