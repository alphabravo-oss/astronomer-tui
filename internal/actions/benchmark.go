//revive:disable:exported package-comments
package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

const (
	MaxBenchmarkRequests    = 10_000
	MaxBenchmarkConcurrency = 100
	MaxBenchmarkDuration    = 5 * time.Minute
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type BenchmarkSpec struct {
	PortForwardID string
	URL           string
	Requests      int
	Concurrency   int
	Timeout       time.Duration
}

type BenchmarkResult struct {
	Completed   int
	Failed      int
	StatusCodes map[int]int
	Min         time.Duration
	Median      time.Duration
	P95         time.Duration
	Max         time.Duration
	Elapsed     time.Duration
	Error       string
}

type Benchmark struct {
	ID       string
	Spec     BenchmarkSpec
	Result   BenchmarkResult
	Status   string
	Started  time.Time
	Finished time.Time
	cancel   context.CancelFunc
}

type BenchmarkRunner func(context.Context, BenchmarkSpec) BenchmarkResult

type BenchmarkManager struct {
	mu     sync.Mutex
	runs   map[string]Benchmark
	nextID uint64
}

func NewBenchmarkManager() *BenchmarkManager {
	return &BenchmarkManager{runs: make(map[string]Benchmark)}
}

func DefaultBenchmarkSpec(forward PortForward) BenchmarkSpec {
	return BenchmarkSpec{
		PortForwardID: forward.ID,
		URL:           fmt.Sprintf("http://127.0.0.1:%d/", forward.Local),
		Requests:      100,
		Concurrency:   10,
		Timeout:       30 * time.Second,
	}
}

func ValidateBenchmarkSpec(spec BenchmarkSpec) error {
	parsed, err := url.Parse(spec.URL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("benchmark URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("benchmark URL cannot contain credentials or a fragment")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("benchmarks are restricted to literal loopback IP port-forwards")
	}
	if spec.Requests <= 0 || spec.Requests > MaxBenchmarkRequests {
		return fmt.Errorf("benchmark requests must be between 1 and %d", MaxBenchmarkRequests)
	}
	if spec.Concurrency <= 0 || spec.Concurrency > MaxBenchmarkConcurrency || spec.Concurrency > spec.Requests {
		return fmt.Errorf("benchmark concurrency must be between 1 and min(requests, %d)", MaxBenchmarkConcurrency)
	}
	if spec.Timeout <= 0 || spec.Timeout > MaxBenchmarkDuration {
		return fmt.Errorf("benchmark timeout must be between 1ns and %s", MaxBenchmarkDuration)
	}
	return nil
}

func RunHTTPBenchmark(ctx context.Context, doer HTTPDoer, spec BenchmarkSpec) BenchmarkResult {
	started := time.Now()
	result := BenchmarkResult{StatusCodes: make(map[int]int)}
	if err := ValidateBenchmarkSpec(spec); err != nil {
		result.Error = err.Error()
		return result
	}
	if doer == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		doer = &http.Client{
			Transport: transport,
			Timeout:   spec.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	type sample struct {
		duration time.Duration
		status   int
		err      error
	}
	jobs := make(chan struct{})
	samples := make(chan sample)
	var workers sync.WaitGroup
	for i := 0; i < spec.Concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				request, err := http.NewRequestWithContext(runCtx, http.MethodGet, spec.URL, nil)
				if err != nil {
					select {
					case samples <- sample{err: err}:
					case <-runCtx.Done():
						return
					}
					continue
				}
				request.Header.Set("User-Agent", "astrotui-benchmark/1")
				requestStarted := time.Now()
				response, err := doer.Do(request)
				elapsed := time.Since(requestStarted)
				status := 0
				if response != nil && response.Body != nil {
					status = response.StatusCode
					_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
					_ = response.Body.Close()
				}
				select {
				case samples <- sample{duration: elapsed, status: status, err: err}:
				case <-runCtx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := 0; i < spec.Requests; i++ {
			select {
			case jobs <- struct{}{}:
			case <-runCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(samples)
	}()

	latencies := make([]time.Duration, 0, spec.Requests)
	for sample := range samples {
		result.Completed++
		if sample.err != nil {
			result.Failed++
		} else {
			result.StatusCodes[sample.status]++
		}
		latencies = append(latencies, sample.duration)
	}
	result.Elapsed = time.Since(started)
	if err := runCtx.Err(); err != nil && result.Completed < spec.Requests {
		result.Error = err.Error()
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		result.Min = latencies[0]
		result.Median = percentile(latencies, 0.50)
		result.P95 = percentile(latencies, 0.95)
		result.Max = latencies[len(latencies)-1]
	}
	return result
}

func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * fraction)
	return sorted[index]
}

func (m *BenchmarkManager) Start(ctx context.Context, spec BenchmarkSpec, runner BenchmarkRunner) (string, error) {
	if err := ValidateBenchmarkSpec(spec); err != nil {
		return "", err
	}
	if runner == nil {
		runner = func(ctx context.Context, spec BenchmarkSpec) BenchmarkResult {
			return RunHTTPBenchmark(ctx, nil, spec)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("bench-%d", m.nextID)
	m.runs[id] = Benchmark{ID: id, Spec: spec, Status: "running", Started: time.Now(), cancel: cancel}
	m.mu.Unlock()
	go func() {
		result := runner(runCtx, spec)
		m.mu.Lock()
		run, ok := m.runs[id]
		if ok {
			run.Result = result
			run.Finished = time.Now()
			run.cancel = nil
			switch {
			case errors.Is(runCtx.Err(), context.Canceled):
				run.Status = "canceled"
			case result.Error != "":
				run.Status = "failed"
			default:
				run.Status = "completed"
			}
			m.runs[id] = run
		}
		m.mu.Unlock()
		cancel()
	}()
	return id, nil
}

func (m *BenchmarkManager) List() []Benchmark {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Benchmark, 0, len(m.runs))
	for _, run := range m.runs {
		run.cancel = nil
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *BenchmarkManager) Cancel(id string) bool {
	m.mu.Lock()
	run, ok := m.runs[id]
	if !ok || run.cancel == nil {
		m.mu.Unlock()
		return false
	}
	cancel := run.cancel
	m.mu.Unlock()
	cancel()
	return true
}

func (m *BenchmarkManager) StopAll() int {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for _, run := range m.runs {
		if run.cancel != nil {
			cancels = append(cancels, run.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}
