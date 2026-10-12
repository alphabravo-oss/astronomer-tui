package ai

import (
	"strings"
	"testing"
)

func TestAIPrivacyBoundary(t *testing.T) {
	for _, secret := range []string{"apiVersion: v1\nkind: Secret\ndata:\n  token: c2VjcmV0", `{"kind":"Secret","data":{"key":"value"}}`} {
		if _, err := Prepare(ManifestRequest(secret)); err == nil {
			t.Fatal("Secret allowed")
		}
	}
	req, err := Prepare(ManifestRequest("apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - name: app\n    env:\n    - name: PASSWORD\n      value: super-private\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.Prompt, "super-private") {
		t.Fatal("env leaked")
	}
	req, err = Prepare(LogSummaryRequest("Authorization: Bearer abc.secret.value\npassword=hunter2\n-----BEGIN PRIVATE KEY-----\nxxx\n-----END PRIVATE KEY-----"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"abc.secret.value", "hunter2", "xxx"} {
		if strings.Contains(req.Prompt, value) {
			t.Fatal("log secret leaked", req.Prompt)
		}
	}
	if _, err = Prepare(LogSummaryRequest(strings.Repeat("x", MaxRequestBytes+1))); err == nil {
		t.Fatal("oversized request allowed")
	}
	for _, parser := range []func([]byte) Chunk{openAIStreamChunk, anthropicStreamChunk} {
		if c := parser([]byte(`{"error":{"message":"quota exceeded"}}`)); c.Err == nil || !c.Done {
			t.Fatal("provider error swallowed")
		}
	}
}
