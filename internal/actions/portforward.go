//revive:disable:exported package-comments
package actions

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type PortForward struct {
	ID        string
	Local     int
	Resource  string
	Remote    int
	Since     time.Time
	Namespace string
	Status    string
	stop      func()
}

type PortForwardSpec struct {
	TargetPortName string
	Namespace      string
	Resource       string
	Local          int
	Remote         int
}

type PortForwardManager struct {
	mu       sync.Mutex
	forwards map[string]PortForward
}

func NewPortForwardManager() *PortForwardManager {
	return &PortForwardManager{forwards: map[string]PortForward{}}
}

func NewPortForward(spec PortForwardSpec, stop func()) PortForward {
	return PortForward{
		ID:        spec.ID(),
		Local:     spec.Local,
		Remote:    spec.Remote,
		Resource:  spec.Resource,
		Namespace: spec.Namespace,
		stop:      stop,
	}
}

func (m *PortForwardManager) Add(forward PortForward) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if forward.Since.IsZero() {
		forward.Since = time.Now()
	}
	if old, ok := m.forwards[forward.ID]; ok && old.stop != nil {
		old.stop()
	}
	m.forwards[forward.ID] = forward
}

func (m *PortForwardManager) List() []PortForward {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PortForward, 0, len(m.forwards))
	for _, forward := range m.forwards {
		out = append(out, forward)
	}
	return out
}

func (m *PortForwardManager) Kill(id string) bool {
	m.mu.Lock()
	forward, ok := m.forwards[id]
	if ok {
		delete(m.forwards, id)
	}
	m.mu.Unlock()
	if ok && forward.stop != nil {
		forward.stop()
	}
	return ok
}

func DefaultPodForwards(pod corev1.Pod) []PortForwardSpec {
	specs := make([]PortForwardSpec, 0)
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.ContainerPort <= 0 {
				continue
			}
			specs = append(specs, PortForwardSpec{
				Namespace: pod.Namespace,
				Resource:  "pod/" + pod.Name,
				Local:     int(port.ContainerPort),
				Remote:    int(port.ContainerPort),
			})
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Remote == specs[j].Remote {
			return specs[i].Resource < specs[j].Resource
		}
		return specs[i].Remote < specs[j].Remote
	})
	return specs
}

func DefaultServiceForwards(service corev1.Service) []PortForwardSpec {
	specs := make([]PortForwardSpec, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		remote := int(port.TargetPort.IntVal)
		if remote == 0 {
			remote = int(port.Port)
		}
		specs = append(specs, PortForwardSpec{
			Namespace:      service.Namespace,
			Resource:       "service/" + service.Name,
			TargetPortName: port.TargetPort.StrVal,
			Local:          int(port.Port),
			Remote:         remote,
		})
	}
	return specs
}

func (p PortForwardSpec) ID() string {
	return fmt.Sprintf("%s:%s:%d:%d", p.Namespace, p.Resource, p.Local, p.Remote)
}

func ResolvePortForwardSpec(ctx context.Context, client kubernetes.Interface, spec PortForwardSpec) (PortForwardSpec, error) {
	if strings.HasPrefix(spec.Resource, "pod/") {
		return spec, nil
	}
	serviceName, ok := strings.CutPrefix(spec.Resource, "service/")
	if !ok || serviceName == "" {
		return PortForwardSpec{}, fmt.Errorf("unsupported port-forward resource %q", spec.Resource)
	}
	if client == nil {
		return PortForwardSpec{}, fmt.Errorf("kubernetes client is required for service port-forward")
	}
	service, err := client.CoreV1().Services(spec.Namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return PortForwardSpec{}, err
	}
	if len(service.Spec.Selector) == 0 {
		return PortForwardSpec{}, fmt.Errorf("service %s has no selector", serviceName)
	}
	pods, err := client.CoreV1().Pods(spec.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String()})
	if err != nil {
		return PortForwardSpec{}, err
	}
	if len(pods.Items) == 0 {
		return PortForwardSpec{}, fmt.Errorf("service %s selector matched no pods", serviceName)
	}
	sort.SliceStable(pods.Items, func(i, j int) bool {
		runningI, runningJ := pods.Items[i].Status.Phase == corev1.PodRunning, pods.Items[j].Status.Phase == corev1.PodRunning
		if runningI != runningJ {
			return runningI
		}
		return pods.Items[i].Name < pods.Items[j].Name
	})
	if spec.TargetPortName != "" {
		found := false
		for _, container := range pods.Items[0].Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == spec.TargetPortName && (port.Protocol == corev1.ProtocolTCP || port.Protocol == "") {
					spec.Remote = int(port.ContainerPort)
					found = true
					break
				}
			}
		}
		if !found {
			return PortForwardSpec{}, fmt.Errorf("pod %s has no TCP port named %s", pods.Items[0].Name, spec.TargetPortName)
		}
	}
	spec.Resource = "pod/" + pods.Items[0].Name
	return spec, nil
}

func RunPortForward(ctx context.Context, cfg *rest.Config, client kubernetes.Interface, spec PortForwardSpec, out, errOut io.Writer) error {
	return RunPortForwardReady(ctx, cfg, client, spec, out, errOut, nil)
}

func RunPortForwardReady(ctx context.Context, cfg *rest.Config, client kubernetes.Interface, spec PortForwardSpec, out, errOut io.Writer, ready func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if cfg == nil {
		return fmt.Errorf("rest config is required for port-forward")
	}
	if client == nil {
		return fmt.Errorf("kubernetes client is required for port-forward")
	}
	resolved, err := ResolvePortForwardSpec(ctx, client, spec)
	if err != nil {
		return err
	}
	spec = resolved
	podName, ok := strings.CutPrefix(spec.Resource, "pod/")
	if !ok || podName == "" {
		return fmt.Errorf("port-forward runner currently requires a pod resource")
	}
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return err
	}
	request := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(spec.Namespace).
		Name(podName).
		SubResource("portforward")
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", request.URL())
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()
	forwarder, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", spec.Local, spec.Remote)}, stopCh, readyCh, out, errOut)
	if err != nil {
		return err
	}
	if ready != nil {
		go func() {
			select {
			case <-ctx.Done():
			case <-readyCh:
				ready()
			}
		}()
	}
	return forwarder.ForwardPorts()
}

func (m *PortForwardManager) TryAdd(forward PortForward) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.forwards {
		if old.Local == forward.Local && old.Status != "stopped" && !strings.HasPrefix(old.Status, "failed:") {
			return fmt.Errorf("local port %d is already registered", forward.Local)
		}
	}
	if forward.Since.IsZero() {
		forward.Since = time.Now()
	}
	forward.Status = "connecting"
	if old, ok := m.forwards[forward.ID]; ok && old.stop != nil {
		old.stop()
	}
	m.forwards[forward.ID] = forward
	return nil
}
func (m *PortForwardManager) SetStatus(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if forward, ok := m.forwards[id]; ok {
		forward.Status = status
		m.forwards[id] = forward
	}
}
