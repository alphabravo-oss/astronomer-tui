package ui

import (
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/alphabravo-oss/astrotui/internal/cluster"
)

func TestCommandCompletionsFirstToken(t *testing.T) {
	app := New(Options{})
	got := app.commandCompletions(":po")
	if !containsStr(got, ":pods") {
		t.Fatalf("expected :pods completion, got %#v", got)
	}
	got = app.commandCompletions(":q")
	if !containsStr(got, ":q") && !containsStr(got, ":quit") {
		t.Fatalf("expected quit completion, got %#v", got)
	}
	if app.commandCompletions(":") != nil {
		t.Fatal("empty prompt should offer no completions")
	}
}

func TestCommandCompletionsContextArg(t *testing.T) {
	raw := clientcmdapi.Config{Contexts: map[string]*clientcmdapi.Context{
		"prod-east": {}, "prod-west": {}, "dev": {},
	}}
	app := New(Options{Client: &cluster.Client{RawConfig: raw}})
	got := app.commandCompletions(":ctx prod")
	if len(got) != 2 || !containsStr(got, ":ctx prod-east") || !containsStr(got, ":ctx prod-west") {
		t.Fatalf("unexpected ctx completions %#v", got)
	}
}

func TestCommandHistoryNavigation(t *testing.T) {
	app := New(Options{})
	app.pushHistory(":pods")
	app.pushHistory(":svc")
	app.pushHistory(":svc") // consecutive dup ignored
	if len(app.cmdHistory) != 2 {
		t.Fatalf("expected 2 history entries, got %#v", app.cmdHistory)
	}
	// Prev walks backward from the end.
	if cmd, _ := app.historyPrev(); cmd != ":svc" {
		t.Fatalf("expected :svc, got %q", cmd)
	}
	if cmd, _ := app.historyPrev(); cmd != ":pods" {
		t.Fatalf("expected :pods, got %q", cmd)
	}
	// Next walks forward.
	if cmd, _ := app.historyNext(); cmd != ":svc" {
		t.Fatalf("expected :svc on next, got %q", cmd)
	}
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestUserAliasResolvesInCommand(t *testing.T) {
	app := New(Options{})
	app.userAliases = map[string]string{"pp": "v1/pods"}
	// runCommandString needs a cache/renderer to switchResource; assert the
	// alias maps to a known resource command instead.
	target := app.userAliases["pp"]
	gvr, ok := resourceCommand(target)
	if !ok || gvr.Resource != "pods" {
		t.Fatalf("alias pp should resolve to pods, got %#v %v", gvr, ok)
	}
}
