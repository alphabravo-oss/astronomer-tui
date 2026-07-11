//revive:disable:exported package-comments blank-imports
package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	cachedmemory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/exec" // registers kubeconfig exec auth plugins
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ClientOptions struct {
	Context           string
	Namespace         string
	AllNamespaces     bool
	ImpersonateUser   string
	ImpersonateUID    string
	ImpersonateGroups []string
}

type Client struct {
	Config            *rest.Config
	Clientset         kubernetes.Interface
	Dynamic           dynamic.Interface
	Discovery         discovery.CachedDiscoveryInterface
	Context           string
	Namespace         string
	KubeconfigPath    string
	RawConfig         clientcmdapi.Config
	RESTConfigQPS     float32
	RESTConfigBurst   int
	ImpersonateUser   string
	ImpersonateUID    string
	ImpersonateGroups []string
}

func ListOptions() metav1.ListOptions {
	return metav1.ListOptions{}
}

func ListNamespaces(ctx context.Context, client *Client) ([]string, error) {
	if client == nil || client.Clientset == nil {
		return nil, nil
	}
	list, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, namespace := range list.Items {
		if namespace.Name != "" {
			names = append(names, namespace.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func NewClient(ctx context.Context, opts ClientOptions) (*Client, error) {
	_ = ctx
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	path := kubeconfigPath(loading)
	overrides := &clientcmd.ConfigOverrides{}
	if opts.Context != "" {
		overrides.CurrentContext = opts.Context
	}
	deferred := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides)
	raw, err := deferred.RawConfig()
	if err != nil {
		return nil, plainConfigError(path, err)
	}
	if raw.CurrentContext == "" {
		return nil, plainConfigError(path, errors.New("current-context is empty"))
	}
	contextName := raw.CurrentContext
	if opts.Context != "" {
		contextName = opts.Context
	}
	restCfg, err := deferred.ClientConfig()
	if err != nil {
		return nil, plainConfigError(path, err)
	}
	restCfg.QPS = 50
	restCfg.Burst = 100
	restCfg.Impersonate.UserName = opts.ImpersonateUser
	restCfg.Impersonate.UID = opts.ImpersonateUID
	restCfg.Impersonate.Groups = append([]string(nil), opts.ImpersonateGroups...)
	ns := opts.Namespace
	if ns == "" && !opts.AllNamespaces {
		if resolved, _, nsErr := deferred.Namespace(); nsErr == nil && resolved != "" {
			ns = resolved
		} else {
			ns = "default"
		}
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}
	return &Client{
		Config:            restCfg,
		Clientset:         clientset,
		Dynamic:           dyn,
		Discovery:         cachedmemory.NewMemCacheClient(disco),
		Context:           contextName,
		Namespace:         ns,
		KubeconfigPath:    path,
		RawConfig:         raw,
		RESTConfigQPS:     restCfg.QPS,
		RESTConfigBurst:   restCfg.Burst,
		ImpersonateUser:   opts.ImpersonateUser,
		ImpersonateUID:    opts.ImpersonateUID,
		ImpersonateGroups: append([]string(nil), opts.ImpersonateGroups...),
	}, nil
}

func kubeconfigPath(rules *clientcmd.ClientConfigLoadingRules) string {
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return env
	}
	if rules.ExplicitPath != "" {
		return rules.ExplicitPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.kube/config"
	}
	return filepath.Join(home, ".kube", "config")
}

func plainConfigError(path string, err error) error {
	return fmt.Errorf("could not load kubeconfig %q: %w", path, err)
}
