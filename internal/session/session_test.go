package session

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	want := State{Context: "kind-kind", Namespace: "default", LastView: "pods", LastFilter: "app=api"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
