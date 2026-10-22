package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// hostile are the characters a recorded request can carry to the terminal that
// prints it: an OSC that sets the window title and ends with BEL, a CSI that
// clears the screen, the same CSI in its one-character C1 form, a carriage
// return that rewrites the line, and a right-to-left override.
var hostile = []string{"\x1b", "\x07", "\u009b", "\r", "\u202e"}

const hostileDetailJSON = `{
  "ID": "abc123",
  "Method": "GET\u001b[2J",
  "Path": "/x\u001b]0;owned\u0007",
  "Status": 200,
  "Duration": "1ms",
  "At": "13:04:11",
  "Findings": ["slow\u009b31m"],
  "Timeline": [{"Name": "sql\r", "Value": "1ms", "Percent": 10}],
  "Queries": [{"SQL": "SELECT \u202egnp.exe", "Args": "\u001b[1A", "Duration": "1ms", "Rows": 1, "Repeated": 1, "Origin": "x.go:1", "Error": "\u001b[31m"}],
  "Dumps": [{"Label": "\u001b]8;;https://evil.example\u0007", "Value": "line one\n\u001b[2Kline two", "At": "1ms", "Origin": "y.go:2"}],
  "External": [{"Method": "GET", "URL": "https://api.example/\u001b[0m", "Status": 200, "Duration": "1ms"}],
  "Events": [{"Name": "paid\u001b[8m", "Payload": "{}", "At": "1ms"}]
}`

const hostileListJSON = `{"Rows": [{"ID": "a\u001b[2J", "Method": "GET", "Path": "/\u001b]0;owned\u0007", "Status": 200,
  "Duration": "1ms", "At": "13:04:11", "Queries": 1, "SQL": "1ms", "NPlusOne": false, "Slow": false}]}`

// assertInert fails when out carries a character a terminal acts on.
func assertInert(t *testing.T, mode, out string) {
	t.Helper()
	for _, c := range hostile {
		if strings.Contains(out, c) {
			t.Errorf("%s printed %q, which the terminal acts on rather than shows:\n%q", mode, c, out)
		}
	}
}

// What the server recorded reaches the terminal as text, in the report, the
// list and --json alike.
func TestTheTerminalIsGivenNoControlCharacters(t *testing.T) {
	out, err := runTrace(t, "abc123", "--host", console(t, http.StatusOK, hostileDetailJSON))
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	assertInert(t, "the report", out)
	for _, want := range []string{`/x\u001b]0;owned\u0007`, `\u202egnp.exe`, "    line one\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not show %q, so what was recorded is not readable:\n%s", want, out)
		}
	}

	out, err = runTrace(t, "--host", console(t, http.StatusOK, hostileListJSON))
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	assertInert(t, "the list", out)

	// --json escapes what JSON allows raw, and stays a document that decodes to
	// what the server sent.
	raw := "{\"Path\": \"/\u009b2J\u202e\"}"
	out, err = runTrace(t, "abc123", "--host", console(t, http.StatusOK, raw), "--json")
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	assertInert(t, "--json", out)
	var decoded struct{ Path string }
	if err := json.Unmarshal([]byte(out), &decoded); err != nil || decoded.Path != "/\u009b2J\u202e" {
		t.Errorf("--json no longer decodes to what the server sent: %v, %q", err, decoded.Path)
	}
}

// A redirect is not followed, so the tracing secret never reaches a host other
// than the one it was given to, and the error says where the answer pointed.
func TestTheTracingSecretDoesNotFollowARedirect(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		if r.Header.Get("X-Arandu-Trace") != "" {
			t.Errorf("the tracing secret reached another host: %q", r.Header.Get("X-Arandu-Trace"))
		}
		_, _ = w.Write([]byte(detailJSON))
	}))
	defer elsewhere.Close()

	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/_arandu/debug/abc123?format=json", status)
		}))

		_, err := runTrace(t, "abc123", "--host", app.URL, "--secret", "s3cret")
		app.Close()
		if err == nil {
			t.Fatalf("%d: a redirected trace was reported as a success", status)
		}
		if !strings.Contains(err.Error(), elsewhere.URL) || !strings.Contains(err.Error(), "does not follow a redirect") {
			t.Errorf("%d: the error does not say where the answer pointed: %v", status, err)
		}
	}
	if reached.Load() {
		t.Error("the redirect was followed")
	}
}
