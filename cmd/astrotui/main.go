//revive:disable:package-comments
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/alphabravo-oss/astrotui/internal/ai"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
	"github.com/alphabravo-oss/astrotui/internal/config"
	"github.com/alphabravo-oss/astrotui/internal/session"
	"github.com/alphabravo-oss/astrotui/internal/ui"
	"github.com/alphabravo-oss/astrotui/internal/ui/theme"
	"github.com/alphabravo-oss/astrotui/internal/ui/views"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	exUsage  = 64
	exConfig = 78
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	started := time.Now()
	if len(args) >= 2 && args[0] == "config" && args[1] == "import-k9s" {
		return runImportK9s(args[2:])
	}

	fs := flag.NewFlagSet("astrotui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	contextName := fs.String("context", "", "Kubernetes context to use")
	namespace := fs.String("namespace", "", "Kubernetes namespace to use")
	noRestore := fs.Bool("no-restore", false, "Skip restoring previous session state")
	printVersion := fs.Bool("version", false, "Print version and exit")
	headless := fs.Bool("headless", false, "Validate configuration and Kubernetes connectivity without starting the TUI")
	headlessRender := fs.Bool("headless-render", false, "Measure initial resource cache sync and render without starting the TUI")
	headlessRenderMinRows := fs.Int("headless-render-min-rows", 0, "Minimum rendered rows required by --headless-render")
	headlessRenderIdle := fs.Duration("headless-render-idle", 0, "Idle after --headless-render and enforce CPU/RSS NFRs over that duration")
	if err := fs.Parse(args); err != nil {
		return exUsage
	}
	if *printVersion {
		fmt.Printf("astrotui %s (%s, %s)\n", version, commit, date)
		return 0
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		var validation *config.ValidationError
		if errors.As(err, &validation) {
			fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
			return exConfig
		}
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return exConfig
	}

	stateStore := session.NewStore(session.DefaultPath())
	var restored session.State
	if !*noRestore {
		if loaded, err := stateStore.Load(); err == nil {
			restored = loaded
			if *contextName == "" {
				*contextName = restored.Context
			}
			if *namespace == "" {
				*namespace = restored.Namespace
			}
		}
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer setupCancel()
	kube, err := cluster.NewClient(setupCtx, cluster.ClientOptions{Context: *contextName, Namespace: *namespace})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exUsage
	}
	if *headless {
		namespaces, listErr := kube.Clientset.CoreV1().Namespaces().List(setupCtx, cluster.ListOptions())
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "failed to list namespaces: %v\n", listErr)
			return 1
		}
		fmt.Printf("connected context=%s namespace=%s namespaces=%d\n", kube.Context, kube.Namespace, len(namespaces.Items))
		return 0
	}
	if *headlessRender {
		return runHeadlessRender(setupCtx, kube, *headlessRenderMinRows, *headlessRenderIdle, started)
	}

	skin, err := theme.Load(theme.DefaultPath(), cfg.Theme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skin error: %v\n", err)
		return exConfig
	}
	aiProvider, err := ai.NewProvider(cfg.AI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai error: %v\n", err)
		return exConfig
	}
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	app := ui.New(ui.Options{
		Client:       kube,
		Config:       cfg,
		SessionStore: stateStore,
		InitialState: restored,
		Skin:         skin,
		SkinUpdates:  theme.Watcher{Path: theme.DefaultPath(), Fallback: cfg.Theme}.Watch(appCtx),
		AIProvider:   aiProvider,
	})
	if err := app.Run(appCtx); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		return 1
	}
	return 0
}

func runHeadlessRender(ctx context.Context, kube *cluster.Client, minRows int, idle time.Duration, started time.Time) int {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	renderStart := time.Now()
	list, err := kube.Dynamic.Resource(gvr).Namespace(kube.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list pods: %v\n", err)
		return 1
	}
	items := make([]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		items[i] = &list.Items[i]
	}
	rows, _ := ui.BuildRows(views.Builtins()[gvr], items, "", 0, false)
	renderElapsed := time.Since(renderStart)
	totalElapsed := time.Since(started)
	if len(rows) < minRows {
		fmt.Fprintf(os.Stderr, "rendered rows=%d below required minimum=%d\n", len(rows), minRows)
		return 1
	}
	idleCPU := time.Duration(0)
	rssBytes := maxRSSBytes()
	if idle > 0 {
		cache := cluster.NewResourceCache(kube.Dynamic, 0)
		cache.Start(ctx)
		if !cache.WaitForSync(ctx, gvr) {
			fmt.Fprintln(os.Stderr, "failed to sync pod informer")
			return 1
		}
		if _, err := cache.List(gvr, kube.Namespace, labels.Everything()); err != nil {
			fmt.Fprintf(os.Stderr, "failed to list cached pods: %v\n", err)
			return 1
		}
		before := processCPUTime()
		time.Sleep(idle)
		idleCPU = processCPUTime() - before
		rssBytes = maxRSSBytes()
		if idleCPU > idle/50 {
			fmt.Fprintf(os.Stderr, "idle cpu used %s over %s, exceeds 2%% budget\n", idleCPU, idle)
			return 1
		}
		if rssBytes > 150*1024*1024 {
			fmt.Fprintf(os.Stderr, "max rss %d bytes exceeds 150MiB\n", rssBytes)
			return 1
		}
	}
	fmt.Printf("headless-render context=%s namespace=%s rows=%d render_ms=%d total_ms=%d idle_ms=%d idle_cpu_ms=%d rss_bytes=%d\n", kube.Context, kube.Namespace, len(rows), renderElapsed.Milliseconds(), totalElapsed.Milliseconds(), idle.Milliseconds(), idleCPU.Milliseconds(), rssBytes)
	return 0
}

func processCPUTime() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	user := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond
	system := time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	return user + system
}

func maxRSSBytes() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return usage.Maxrss
	}
	return usage.Maxrss * 1024
}

func runImportK9s(args []string) int {
	fs := flag.NewFlagSet("astrotui config import-k9s", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	src := fs.String("from", config.DefaultK9sDir(), "k9s config directory")
	dst := fs.String("to", config.DefaultDir(), "astrotui config directory")
	if err := fs.Parse(args); err != nil {
		return exUsage
	}
	results, err := config.ImportK9s(*src, *dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import failed: %v\n", err)
		return exConfig
	}
	for _, result := range results {
		fmt.Printf("%s: %s\n", result.File, result.Status)
	}
	return 0
}
