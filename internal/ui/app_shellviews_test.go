package ui

import "testing"

func TestParseHelmList(t *testing.T) {
	data := []byte(`[
	  {"name":"web","namespace":"prod","revision":"3","status":"deployed","chart":"web-1.2.3","app_version":"1.0"},
	  {"name":"api","namespace":"prod","revision":"1","status":"failed","chart":"api-0.1.0","app_version":"0.1"}
	]`)
	releases, err := parseHelmList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}
	// Sorted by namespace then name -> api before web.
	if releases[0].Name != "api" || releases[1].Name != "web" {
		t.Fatalf("unexpected order %#v", releases)
	}
	if releases[0].Status != "failed" || releases[1].Chart != "web-1.2.3" {
		t.Fatalf("unexpected fields %#v", releases)
	}
	if _, err := parseHelmList([]byte("not json")); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParsePopeye(t *testing.T) {
	data := []byte(`{"popeye":{"score":85,"grade":"B","sanitizers":[
	  {"sanitizer":"pods","tally":{"ok":10,"info":1,"warning":2,"error":3}},
	  {"sanitizer":"deployments","tally":{"ok":5,"info":0,"warning":0,"error":0}}
	]}}`)
	rows, grade, score, err := parsePopeye(data)
	if err != nil {
		t.Fatal(err)
	}
	if grade != "B" || score != 85 {
		t.Fatalf("unexpected grade/score %s/%d", grade, score)
	}
	if len(rows) != 2 || rows[0].Sanitizer != "deployments" || rows[1].Sanitizer != "pods" {
		t.Fatalf("unexpected sorted rows %#v", rows)
	}
	if rows[1].Warn != 2 || rows[1].Errs != 3 {
		t.Fatalf("unexpected pod tally %#v", rows[1])
	}
}

func TestParseHelmAndPopeyeCommands(t *testing.T) {
	if cmd, ok := parseCommand(":helm"); !ok || cmd.Kind != "helm" {
		t.Fatalf("unexpected helm command %#v %v", cmd, ok)
	}
	if cmd, ok := parseCommand(":popeye"); !ok || cmd.Kind != "popeye" {
		t.Fatalf("unexpected popeye command %#v %v", cmd, ok)
	}
}
