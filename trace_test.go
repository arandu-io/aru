package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The CLI reads the running application's console over HTTP, so the tests point
// it at a server that answers the way the console does. The console's own tests
// live in the framework; these are about what reaches the terminal.

const detailJSON = `{
  "ID": "abc123",
  "Method": "GET",
  "Path": "/customers",
  "Status": 200,
  "Duration": "120ms",
  "At": "13:04:11",
  "Findings": ["Likely N+1: the same statement ran 8 times — SELECT * FROM invoice WHERE customer_id = ?"],
  "Timeline": [
    {"Name": "sql", "Value": "90ms", "Percent": 75},
    {"Name": "render", "Value": "20ms", "Percent": 16},
    {"Name": "external", "Value": "0.00ms", "Percent": 0},
    {"Name": "other", "Value": "10ms", "Percent": 8}
  ],
  "Queries": [
    {"SQL": "SELECT * FROM invoice WHERE customer_id = ?", "Args": "c-1", "Duration": "11ms",
     "Rows": 1, "Repeated": 8, "Origin": "invoice/invoice.repo.go:98", "Error": ""}
  ],
  "Dumps": [{"Label": "the customer", "Value": "{\n  \"id\": \"c-1\"\n}", "At": "3ms", "Origin": "customer/handlers.go:41"}],
  "External": [{"Method": "GET", "URL": "https://api.example/rates", "Status": 200, "Duration": "40ms"}],
  "Events": [{"Name": "invoice.paid", "Payload": "{}", "At": "80ms"}]
}`

const listJSON = `{
  "Rows": [
    {"ID": "abc123", "Method": "GET", "Path": "/customers", "Status": 200,
     "Duration": "120ms", "At": "13:04:11", "Queries": 9, "SQL": "90ms", "NPlusOne": true, "Slow": false},
    {"ID": "def456", "Method": "POST", "Path": "/auth/login", "Status": 401,
     "Duration": "8ms", "At": "13:04:20", "Queries": 1, "SQL": "2ms", "NPlusOne": false, "Slow": false}
  ]
}`

// console returns a stand-in for the running application.
func console(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("the CLI asked for %s, and should always ask for JSON", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runTrace calls the command the way main does. The name avoids the run() that
// main.go already has: this package is one package, and the test files share it.
func runTrace(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut strings.Builder
	err := trace(args, &out, &errOut)
	return out.String(), err
}

// TestTheDiagnosisComesFirst: the reason to run this command is the finding, and
// a report that buries it under a query table is a report people skim past.
func TestTheDiagnosisComesFirst(t *testing.T) {
	host := console(t, http.StatusOK, detailJSON)

	out, err := runTrace(t, "abc123", "--host", host)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	if !strings.Contains(out, "Likely N+1") {
		t.Fatalf("the finding is not in the report:\n%s", out)
	}
	if strings.Index(out, "Likely N+1") > strings.Index(out, "queries (") {
		t.Error("the finding is printed below the query list")
	}
}

func TestTheReportShowsTheTimelineAndTheOrigin(t *testing.T) {
	host := console(t, http.StatusOK, detailJSON)

	out, err := runTrace(t, "abc123", "--host", host)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	for _, want := range []string{
		"GET /customers", "120ms", "abc123",
		"sql", "90ms", "75%",
		"invoice/invoice.repo.go:98", // the line that saves the time
		"x8",                         // the repetition, next to the query
		"the customer",               // the dump
		"https://api.example/rates",  // the outbound call
		"invoice.paid",               // the event
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not show %q:\n%s", want, out)
		}
	}
}

func TestTheListIsPrintedWithoutAnId(t *testing.T) {
	host := console(t, http.StatusOK, listJSON)

	out, err := runTrace(t, "--host", host)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	for _, want := range []string{"abc123", "/customers", "def456", "/auth/login", "401", "N+1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list does not show %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "aru trace <id>") {
		t.Error("the list does not say how to open one of them")
	}
}

func TestAnEmptyListSaysSo(t *testing.T) {
	host := console(t, http.StatusOK, `{"Rows": []}`)

	out, err := runTrace(t, "--host", host)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if !strings.Contains(out, "nothing recorded yet") {
		t.Errorf("an empty buffer is not explained:\n%s", out)
	}
}

// TestAServerThatIsNotThereSaysWhatToDo: this is the most common failure of
// this command, and what net/http says on its own names a port and a syscall.
func TestAServerThatIsNotThereSaysWhatToDo(t *testing.T) {
	// Port 1 refuses connections everywhere.
	_, err := runTrace(t, "abc123", "--host", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("connecting to nothing succeeded")
	}
	if !strings.Contains(err.Error(), "aru dev") {
		t.Errorf("the error does not say how to start the application: %v", err)
	}
}

// TestAnUnknownIdIsDistinguishedFromAMissingConsole: both are 404, and they need
// different fixes -- one is "reproduce the request", the other is "you are not
// in development".
func TestAnUnknownIdIsDistinguishedFromAMissingConsole(t *testing.T) {
	unknown := console(t, http.StatusNotFound, `{"error": "no request with that id", "id": "nope", "kept": 3}`)
	if _, err := runTrace(t, "nope", "--host", unknown); err == nil || !strings.Contains(err.Error(), "reproduce it") {
		t.Errorf("an unknown id: %v", err)
	}

	absent := console(t, http.StatusNotFound, `404 page not found`)
	if _, err := runTrace(t, "nope", "--host", absent); err == nil || !strings.Contains(err.Error(), "no debug console") {
		t.Errorf("a console that is not mounted: %v", err)
	}
}

// TestTheTracingSecretIsSent: production tracing on demand only works if the
// CLI passes the header the middleware checks.
func TestTheTracingSecretIsSent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Arandu-Trace")
		_, _ = w.Write([]byte(detailJSON))
	}))
	defer srv.Close()

	if _, err := runTrace(t, "abc123", "--host", srv.URL, "--secret", "s3cret"); err != nil {
		t.Fatalf("trace: %v", err)
	}
	if got != "s3cret" {
		t.Errorf("the header carried %q", got)
	}
}

// TestNoSecretMeansNoHeader: sending an empty one would be a request that looks
// like an attempt to guess it.
func TestNoSecretMeansNoHeader(t *testing.T) {
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["X-Arandu-Trace"]
		_, _ = w.Write([]byte(detailJSON))
	}))
	defer srv.Close()

	if _, err := runTrace(t, "abc123", "--host", srv.URL); err != nil {
		t.Fatalf("trace: %v", err)
	}
	if present {
		t.Error("an empty tracing header was sent")
	}
}

func TestTheRawJSONIsAvailable(t *testing.T) {
	host := console(t, http.StatusOK, detailJSON)

	out, err := runTrace(t, "abc123", "--host", host, "--json")
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if !strings.Contains(out, `"Findings"`) {
		t.Errorf("--json did not print the response:\n%s", out)
	}
}

// TestGarbageFromTheServerIsNotAPanic: the host might be pointing at something
// else entirely, and a stack trace would be a worse answer than a sentence.
func TestGarbageFromTheServerIsNotAPanic(t *testing.T) {
	host := console(t, http.StatusOK, `<!doctype html><h1>some other server</h1>`)

	if _, err := runTrace(t, "abc123", "--host", host); err == nil {
		t.Fatal("an HTML response was accepted as a trace")
	}
}

func TestTheBarIsProportional(t *testing.T) {
	for percent, want := range map[int]string{
		0:   strings.Repeat(".", 20),
		50:  strings.Repeat("#", 10) + strings.Repeat(".", 10),
		100: strings.Repeat("#", 20),
		// Parts can exceed the whole when goroutines overlap; the bar clamps
		// rather than drawing past its own width.
		140: strings.Repeat("#", 20),
	} {
		if got := bar(percent); got != want {
			t.Errorf("bar(%d) = %q, want %q", percent, got, want)
		}
	}
}
