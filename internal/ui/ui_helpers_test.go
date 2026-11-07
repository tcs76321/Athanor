package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModelCall(t *testing.T) {
	ev := eventView{DataJSON: `{
		"phase":"diverging","persona":"main","model":"m","prompt":10,
		"completion":20,"reasoning":3,"temperature":0.7,"max_tokens":4096,
		"budget_sec":300,"think":"high","response":"out","thinking":"why"
	}`}
	got := parseModelCall(ev)
	if got.Phase != "diverging" || got.Persona != "main" || got.Model != "m" {
		t.Fatalf("identity fields = %+v", got)
	}
	if got.Prompt != 10 || got.Completion != 20 || got.Reasoning != 3 {
		t.Fatalf("token fields = %+v", got)
	}
	if got.Think != "high" || got.Response != "out" || got.Thinking != "why" {
		t.Fatalf("think/response fields = %+v", got)
	}
}

func TestParseModelCallDefaults(t *testing.T) {
	if got := parseModelCall(eventView{DataJSON: `{}`}); got.Think != "off" {
		t.Fatalf("think = %q, want off", got.Think)
	}
	if got := parseModelCall(eventView{DataJSON: `{"think":true}`}); got.Think != "on" {
		t.Fatalf("think = %q, want on", got.Think)
	}
	if got := parseModelCall(eventView{DataJSON: `not json`}); got.Think != "off" {
		t.Fatalf("invalid JSON think = %q, want off", got.Think)
	}
}

func TestParseUnits(t *testing.T) {
	got := parseUnits(`{"units_detail":[{"id":"u1","goal":"g"}]}`)
	if len(got) != 1 || got[0].ID != "u1" || got[0].Goal != "g" {
		t.Fatalf("units = %+v", got)
	}
	if got := parseUnits(`{}`); len(got) != 0 {
		t.Fatalf("empty units = %+v", got)
	}
	if got := parseUnits(`not json`); len(got) != 0 {
		t.Fatalf("invalid units = %+v", got)
	}
}

func TestParsePassed(t *testing.T) {
	cases := map[string]string{
		`{"passed":true}`:  "pass",
		`{"passed":false}`: "fail",
		`{}`:               "",
		`{"passed":null}`:  "",
		`not json`:         "",
	}
	for raw, want := range cases {
		if got := parsePassed(raw); got != want {
			t.Errorf("parsePassed(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("short"); got != "short" {
		t.Errorf("short = %q", got)
	}
	long := "0123456789abcdef"
	if got := shortID(long); got != "01234567…" {
		t.Errorf("long = %q", got)
	}
}

func TestMustJSON(t *testing.T) {
	if got := mustJSON(map[string]int{"n": 1}); got != `{"n":1}` {
		t.Errorf("mustJSON = %q", got)
	}
	if got := mustJSON(make(chan int)); got != `""` {
		t.Errorf("unsupported value = %q, want empty JSON string", got)
	}
}

func TestWriteSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSE(rec, "phase", `{"phase":"diverging"}`)
	body := rec.Body.String()
	if !strings.Contains(body, "event: phase") || !strings.Contains(body, "data: {\"phase\":\"diverging\"}") {
		t.Fatalf("SSE body = %q", body)
	}
}
