package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/benchmark"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/safety"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type benchmarkSettings struct {
	Requests    int `yaml:"requests"`
	Concurrency int `yaml:"concurrency"`
	HTTP        struct {
		Path, Method, Host string
		Body               any
		Header             map[string]string
	} `yaml:"http"`
	Auth struct{ User, Password string } `yaml:"auth"`
}

func (a *App) showBenchmark(endpoint string) {
	spec := benchmark.Spec{URL: endpoint, Requests: 200, Concurrency: 1, Method: "GET"}
	if endpoint == "" {
		spec.URL = "http://127.0.0.1:8080/"
	}
	var cfg struct {
		Benchmarks struct {
			Defaults   benchmarkSettings            `yaml:"defaults"`
			Services   map[string]benchmarkSettings `yaml:"services"`
			Containers map[string]benchmarkSettings `yaml:"containers"`
		} `yaml:"benchmarks"`
	}
	// Files are read when opening the form, so edits apply to the next explicit run.
	path := filepath.Join(a.customDir(), "benchmarks.yaml")
	if c := a.options.Client; c != nil {
		if selected := c.RawConfig.Contexts[c.Context]; selected != nil {
			scoped := filepath.Join(config.ContextDir(a.customDir(), selected.Cluster, c.Context), "benchmarks.yaml")
			if _, err := os.Stat(scoped); err == nil {
				path = scoped
			} else if !os.IsNotExist(err) {
				a.showMessage("Benchmark config", err.Error())
				return
			}
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			a.showMessage("Benchmark config", err.Error())
			return
		}
		settings := cfg.Benchmarks.Defaults
		apply := func(v benchmarkSettings) {
			if v.Requests > 0 {
				settings.Requests = v.Requests
			}
			if v.Concurrency > 0 {
				settings.Concurrency = v.Concurrency
			}
			if v.HTTP.Method != "" {
				settings.HTTP.Method = v.HTTP.Method
			}
			if v.HTTP.Path != "" {
				settings.HTTP.Path = v.HTTP.Path
			}
			if v.HTTP.Host != "" {
				settings.HTTP.Host = v.HTTP.Host
			}
			if v.HTTP.Body != nil {
				settings.HTTP.Body = v.HTTP.Body
			}
			if v.HTTP.Header != nil {
				settings.HTTP.Header = v.HTTP.Header
			}
			if v.Auth.User != "" {
				settings.Auth = v.Auth
			}
		}
		if obj := a.selectedObject(); obj != nil {
			key := obj.GetNamespace() + "/" + obj.GetName()
			if obj.GetKind() == "Service" {
				apply(cfg.Benchmarks.Services[key])
			}
			if obj.GetKind() == "Pod" {
				containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
				if len(containers) > 0 {
					if c, ok := containers[0].(map[string]any); ok {
						apply(cfg.Benchmarks.Containers[key+":"+fmt.Sprint(c["name"])])
					}
				}
			}
		}
		if settings.Requests > 0 {
			spec.Requests = settings.Requests
		}
		if settings.Concurrency > 0 {
			spec.Concurrency = settings.Concurrency
		}
		if settings.HTTP.Method != "" {
			spec.Method = settings.HTTP.Method
		}
		spec.Headers = settings.HTTP.Header
		if settings.HTTP.Body != nil {
			if body, ok := settings.HTTP.Body.(string); ok {
				spec.Body = body
			} else {
				b, err := json.Marshal(settings.HTTP.Body)
				if err != nil {
					a.showMessage("Benchmark body", err.Error())
					return
				}
				spec.Body = string(b)
			}
		}
		if u, err := url.Parse(spec.URL); err == nil {
			if endpoint == "" && settings.HTTP.Host != "" {
				u.Host = settings.HTTP.Host
			}
			if settings.HTTP.Path != "" {
				relative, err := url.Parse(settings.HTTP.Path)
				if err != nil {
					a.showMessage("Benchmark path", err.Error())
					return
				}
				u.Path = relative.Path
				u.RawQuery = relative.RawQuery
			}
			if settings.Auth.User != "" {
				u.User = url.UserPassword(settings.Auth.User, settings.Auth.Password)
			}
			spec.URL = u.String()
		}
	} else if !os.IsNotExist(err) {
		a.showMessage("Benchmark config", err.Error())
		return
	}
	form := tview.NewForm()
	form.AddInputField("Endpoint", spec.URL, 70, nil, func(v string) { spec.URL = v })
	form.AddInputField("Requests (1–10000)", strconv.Itoa(spec.Requests), 12, tview.InputFieldInteger, func(v string) { spec.Requests, _ = strconv.Atoi(v) })
	form.AddInputField("Concurrency (1–64)", strconv.Itoa(spec.Concurrency), 12, tview.InputFieldInteger, func(v string) { spec.Concurrency, _ = strconv.Atoi(v) })
	form.AddInputField("HTTP method", spec.Method, 12, nil, func(v string) { spec.Method = strings.ToUpper(v) })
	form.AddTextView("Configured payload", fmt.Sprintf("Body %d bytes; %d headers. 60s total limit; redirects are not followed.", len(spec.Body), len(spec.Headers)), 70, 2, false, false)
	form.AddButton("Cancel", a.showMain)
	form.AddButton("Review run", func() {
		if err := spec.Validate(); err != nil {
			a.showMessage("Benchmark", err.Error())
			return
		}
		a.confirmExternalMutation("Benchmark", fmt.Sprintf("Send up to %d %s requests, concurrency %d, to %s?\nBody %d bytes, %d headers. This creates real HTTP traffic.", spec.Requests, spec.Method, spec.Concurrency, spec.URL, len(spec.Body), len(spec.Headers)), func() {
			dir := filepath.Join(a.customDir(), "benchmarks")
			contextName := a.options.Client.Context
			a.operationWithTimeout("Benchmark endpoint", 65*time.Second, func(ctx context.Context) (func(), error) {
				report, err := benchmark.Run(ctx, spec)
				if err != nil {
					return nil, err
				}
				report.Context = contextName
				file, err := benchmark.Save(dir, report)
				if err != nil {
					return nil, err
				}
				return func() { a.showDocument("Benchmark report · "+filepath.Base(file), benchmark.Summary(report)) }, nil
			})
		})
	})
	form.SetTitle(" HTTP benchmark · no traffic until confirmed ").SetBorder(true)
	form.SetCancelFunc(a.showMain)
	a.app.SetRoot(form, true).SetFocus(form)
}

// Applies the same policy to new integrations as built-in Kubernetes mutations.
func (a *App) confirmExternalMutation(title, description string, run func()) {
	contextName := a.options.Client.Context
	cfg := a.options.Config
	if safety.PolicyFor(cfg, contextName) == "read-only" {
		a.showMessage("Action blocked", safety.ErrReadOnly.Error())
		return
	}
	modal := tview.NewModal().SetText(tview.Escape(description)).AddButtons([]string{"Cancel", "Continue"})
	modal.SetDoneFunc(func(i int, _ string) {
		if i != 1 {
			a.showMain()
			return
		}
		if safety.RequiresConfirmation(cfg, contextName, safety.Patch) {
			input := tview.NewInputField().SetLabel("Type context " + contextName + ": ")
			input.SetDoneFunc(func(key tcell.Key) {
				if key != tcell.KeyEnter {
					a.showMain()
					return
				}
				if err := safety.Authorize(a.options.Config, contextName, safety.Patch, input.GetText()); err != nil {
					a.showMessage(title, err.Error())
					return
				}
				run()
			})
			a.app.SetRoot(input, true).SetFocus(input)
		} else {
			if err := safety.Authorize(a.options.Config, contextName, safety.Patch, ""); err != nil {
				a.showMessage(title, err.Error())
				return
			}
			run()
		}
	})
	a.app.SetRoot(modal, false).SetFocus(modal)
}
