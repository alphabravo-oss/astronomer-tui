package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/alphabravo-oss/astrotui/internal/actions"
	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func TestDecodeSecretBody(t *testing.T) {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"user":   []byte("admin"),
			"binary": {0xff, 0xfe, 0x00},
		},
	}
	body := decodeSecretBody(secret)
	if !strings.Contains(body, "user:\nadmin") {
		t.Fatalf("expected decoded user value, got:\n%s", body)
	}
	if !strings.Contains(body, "<binary: 3 bytes") {
		t.Fatalf("expected binary marker, got:\n%s", body)
	}
	if strings.Contains(body, "YWRtaW4=") {
		t.Fatal("utf8 value should be shown decoded, not base64")
	}
}

func TestKillResourceUsesZeroGrace(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), podObject("api", "default"))
	if err := actions.KillResource(context.Background(), client, gvr, "default", "api"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, _ := client.Resource(gvr).Namespace("default").List(ctx, cluster.ListOptions())
	if len(list.Items) != 0 {
		t.Fatalf("expected pod killed, %d remain", len(list.Items))
	}
}
