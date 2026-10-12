// Package benchmark runs explicit, bounded HTTP load tests. It never starts a
// load test as a side effect of opening a resource or report.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Spec struct {
	URL         string            `json:"url" yaml:"url"`
	Requests    int               `json:"requests" yaml:"requests"`
	Concurrency int               `json:"concurrency" yaml:"concurrency"`
	Method      string            `json:"method" yaml:"method"`
	Body        string            `json:"-" yaml:"body"`
	Headers     map[string]string `json:"-" yaml:"headers"`
	Timeout     time.Duration     `json:"-" yaml:"-"`
}
type Report struct {
	Context                                                                             string    `json:"context"`
	Started                                                                             time.Time `json:"started"`
	Endpoint                                                                            string    `json:"endpoint"`
	Method                                                                              string    `json:"method"`
	Requested, Concurrency, Completed, Failed                                           int
	Cancelled                                                                           bool
	ElapsedSeconds, RequestsPerSecond, MinMS, MeanMS, P50MS, P90MS, P95MS, P99MS, MaxMS float64
	StatusCodes                                                                         map[int]int
	Errors                                                                              map[string]int
}

func (s Spec) Validate() error {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("endpoint must be an absolute http(s) URL")
	}
	if s.Requests < 1 || s.Requests > 10000 {
		return fmt.Errorf("requests must be 1–10000")
	}
	if s.Concurrency < 1 || s.Concurrency > 64 || s.Concurrency > s.Requests {
		return fmt.Errorf("concurrency must be 1–64 and no greater than requests")
	}
	if len(s.Body) > 1024*1024 {
		return fmt.Errorf("request body exceeds 1 MiB")
	}
	if s.Method == "" {
		return fmt.Errorf("HTTP method is required")
	}
	_, err = http.NewRequest(s.Method, s.URL, nil)
	return err
}
func Run(ctx context.Context, spec Spec) (Report, error) {
	if err := spec.Validate(); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	timeout := spec.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = spec.Concurrency
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u, _ := url.Parse(spec.URL)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	result := Report{Started: time.Now().UTC(), Endpoint: u.String(), Method: spec.Method, Requested: spec.Requests, Concurrency: spec.Concurrency, StatusCodes: map[int]int{}, Errors: map[string]int{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := 0
	var elapsed []float64
	for i := 0; i < spec.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if next >= spec.Requests || ctx.Err() != nil {
					mu.Unlock()
					return
				}
				next++
				mu.Unlock()
				req, err := http.NewRequestWithContext(ctx, spec.Method, spec.URL, strings.NewReader(spec.Body))
				if err != nil {
					return
				}
				for key, value := range spec.Headers {
					req.Header.Set(key, value)
				}
				start := time.Now()
				resp, err := client.Do(req)
				status := 0
				if err == nil {
					status = resp.StatusCode
					n, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024+1))
					if n > 1024*1024 {
						readErr = fmt.Errorf("response exceeds 1 MiB")
					}
					_ = resp.Body.Close()
					if readErr != nil {
						err = readErr
					}
				}
				ms := float64(time.Since(start)) / float64(time.Millisecond)
				mu.Lock()
				result.Completed++
				elapsed = append(elapsed, ms)
				if status != 0 {
					result.StatusCodes[status]++
				}
				if err != nil {
					result.Failed++
					reason := "transport/read error"
					if ctx.Err() != nil {
						reason = "cancelled"
					}
					result.Errors[reason]++
				} else if status >= 400 {
					result.Failed++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	result.ElapsedSeconds = time.Since(result.Started).Seconds()
	result.Cancelled = ctx.Err() != nil
	if result.ElapsedSeconds > 0 {
		result.RequestsPerSecond = float64(result.Completed) / result.ElapsedSeconds
	}
	sort.Float64s(elapsed)
	if len(elapsed) > 0 {
		percentile := func(p float64) float64 { return elapsed[int(math.Ceil(float64(len(elapsed))*p))-1] }
		result.MinMS = elapsed[0]
		result.MaxMS = elapsed[len(elapsed)-1]
		result.P50MS = percentile(.50)
		result.P90MS = percentile(.90)
		result.P95MS = percentile(.95)
		result.P99MS = percentile(.99)
		for _, ms := range elapsed {
			result.MeanMS += ms
		}
		result.MeanMS /= float64(len(elapsed))
	}
	return result, nil
}
func Save(dir string, r Report) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".benchmark-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(r); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	name := "benchmark-" + r.Started.Format("20060102T150405.000000000") + ".json"
	path := filepath.Join(dir, name)
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
func Summary(r Report) string {
	var b strings.Builder
	state := "COMPLETE"
	if r.Cancelled {
		state = "CANCELLED · partial results"
	}
	fmt.Fprintf(&b, "%s\n\n%s %s\nContext: %s\nStarted: %s\n\nREQUESTS\n  Completed     %d / %d\n  Failed        %d\n  Concurrency   %d\n  Duration      %.3f s\n  Throughput    %.2f requests/s\n\nLATENCY (ms)\n  Min       Mean      p50       p90       p95       p99       Max\n  %-9.3f %-9.3f %-9.3f %-9.3f %-9.3f %-9.3f %.3f\n\nHTTP STATUS\n", state, r.Method, r.Endpoint, r.Context, r.Started.Format(time.RFC3339), r.Completed, r.Requested, r.Failed, r.Concurrency, r.ElapsedSeconds, r.RequestsPerSecond, r.MinMS, r.MeanMS, r.P50MS, r.P90MS, r.P95MS, r.P99MS, r.MaxMS)
	codes := make([]int, 0, len(r.StatusCodes))
	for code := range r.StatusCodes {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		fmt.Fprintf(&b, "  %d %-24s %d\n", code, http.StatusText(code), r.StatusCodes[code])
	}
	if len(codes) == 0 {
		b.WriteString("  No HTTP responses\n")
	}
	if len(r.Errors) > 0 {
		b.WriteString("\nERRORS\n")
		var reasons []string
		for reason := range r.Errors {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			fmt.Fprintf(&b, "  %s: %d\n", reason, r.Errors[reason])
		}
	}
	return b.String()
}
