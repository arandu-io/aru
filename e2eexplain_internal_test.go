package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	e2etrace "github.com/arandu-io/hesape/arandutest/trace"
)

// failedTrace writes a trace the way a journey writes one when the server
// answered 500 to the form it submitted: the step that waited for "Paid"
// failed, the page reported the 500, and the server's side says why.
func failedTrace(t *testing.T, dir string) {
	t.Helper()
	rec := e2etrace.NewRecorder("TestInvoiceIsPaid")
	rec.SetDriver("cdp", "chromium")
	now := time.Now()
	rec.Step(e2etrace.Step{Action: "visit", Target: "invoices.show", URL: "http://127.0.0.1:9/invoices/7", Started: now}, []byte("png"), []byte("<html></html>"))
	rec.Step(e2etrace.Step{Action: "click", Target: `role=button name="Confirm"`, URL: "http://127.0.0.1:9/invoices/7", Started: now}, nil, nil)
	rec.Step(e2etrace.Step{Action: "expect text", Target: "Paid", URL: "http://127.0.0.1:9/invoices/7", Started: now,
		Error: `"Paid" never appeared; the page reads "Invoice 7 Open"`}, []byte("png"), []byte("<html></html>"))
	rec.Request(e2etrace.Request{ID: "1", Method: "GET", URL: "http://127.0.0.1:9/invoices/7", Status: 200, ServerRequestID: "req-a"})
	rec.Request(e2etrace.Request{ID: "2", Method: "POST", URL: "http://127.0.0.1:9/invoices/7/pay", Status: 500, ServerRequestID: "req-b"})
	rec.Console(e2etrace.Message{Level: "error", Text: "Failed to load resource: the server responded with a status of 500"})
	rec.Fail(e2etrace.Failure{Time: now, Kind: "server-error", Text: "POST http://127.0.0.1:9/invoices/7/pay answered 500"})
	rec.Server("req-b", e2etrace.Server{
		Logs:      []e2etrace.LogLine{{Level: "info", Message: "request started"}, {Level: "error", Message: "card refused", Attrs: map[string]any{"card": "4000"}}},
		Queries:   []e2etrace.Query{{SQL: "UPDATE invoices\n  SET paid_at = $1", Error: "column paid_at does not exist"}},
		Decisions: []e2etrace.Decision{{Policy: "InvoicePolicy", Action: "invoice.pay", Allowed: true, Grant: "invoice.pay@t1"}},
	})
	if err := rec.Write(dir, true); err != nil {
		t.Fatal(err)
	}
}

// TestExplainPutsTheServerErrorFirst reads a failed trace through the command
// and requires the cause -- the 500 and what the server did for it -- ahead of
// the step whose expectation it broke.
func TestExplainPutsTheServerErrorFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "TestInvoiceIsPaid")
	failedTrace(t, dir)

	code, stdout, stderr := exercise(t, "e2e:explain", dir)
	if code != 0 {
		t.Fatalf("e2e:explain exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"TestInvoiceIsPaid (cdp, chromium) failed",
		"Probable cause: the server answered 500 to POST http://127.0.0.1:9/invoices/7/pay",
		"log ERROR card refused card=4000",
		"sql UPDATE invoices SET paid_at = $1 -- error: column paid_at does not exist",
		"policy InvoicePolicy allowed invoice.pay (invoice.pay@t1)",
		"Failing step: 3 expect text Paid",
		filepath.Join(dir, "steps", "003-expect-text.png"),
		"(request req-b)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the explanation does not say %q:\n%s", want, stdout)
		}
	}
	cause := strings.Index(stdout, "Probable cause")
	step := strings.Index(stdout, "Failing step")
	if cause < 0 || step < 0 || cause > step {
		t.Errorf("the cause is not ahead of the failing step:\n%s", stdout)
	}
	if strings.Index(stdout, "log ERROR") > strings.Index(stdout, "log INFO") {
		t.Errorf("the server's error line is not ahead of its other lines:\n%s", stdout)
	}

	code, stdout, _ = exercise(t, "e2e:explain", dir, "--json")
	var e explanation
	if code != 0 || json.Unmarshal([]byte(stdout), &e) != nil || !strings.Contains(e.Cause, "answered 500") {
		t.Errorf("--json did not answer the explanation: %s", stdout)
	}
}

// TestExplainOrdersTheCauses walks the causes in the order the trace is read:
// a refusal before an exception, an exception before a console error, and the
// failing step only when nothing under it explains it.
func TestExplainOrdersTheCauses(t *testing.T) {
	step := e2etrace.Step{Index: 2, Action: "click", Target: `role=button name="Pay"`, Error: "no visible element"}
	refused := &e2etrace.Server{Decisions: []e2etrace.Decision{{Policy: "InvoicePolicy", Action: "invoice.pay", Reason: "not the owner"}}}
	for _, c := range []struct {
		name  string
		trace e2etrace.Trace
		want  string
	}{
		{"refusal", e2etrace.Trace{Failed: true, Steps: []e2etrace.Step{step},
			Network: []e2etrace.Request{{ID: "1", Method: "POST", URL: "/pay", Status: 403, ServerRequestID: "r"}},
			Server:  map[string]*e2etrace.Server{"r": refused},
			Console: []e2etrace.Message{{Level: "error", Text: "boom"}}}, "InvoicePolicy refused invoice.pay to POST /pay: not the owner"},
		{"exception", e2etrace.Trace{Failed: true, Steps: []e2etrace.Step{step},
			Failures: []e2etrace.Failure{{Kind: "console-error", Text: "x"}, {Kind: "exception", Text: "uncaught exception: TypeError"}}},
			"the page threw an exception it did not catch: uncaught exception: TypeError"},
		{"console", e2etrace.Trace{Failed: true, Steps: []e2etrace.Step{step}, Console: []e2etrace.Message{{Level: "error", Text: "boom"}}},
			"the page wrote an error to the console: boom"},
		{"no answer", e2etrace.Trace{Failed: true, Network: []e2etrace.Request{{Method: "GET", URL: "/x", Error: "net::ERR_CONNECTION_REFUSED"}}},
			"GET /x got no answer: net::ERR_CONNECTION_REFUSED"},
		{"step", e2etrace.Trace{Failed: true, Steps: []e2etrace.Step{step}}, `step 2, click role=button name="Pay", did not hold: no visible element`},
		{"passed", e2etrace.Trace{}, "the test passed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := explainTrace(&c.trace, "dir").Cause; !strings.Contains(got, c.want) {
				t.Errorf("cause = %q, want %q", got, c.want)
			}
		})
	}
}

// TestExplainFindsTheLatestFailedTrace runs the command with no directory and
// requires the failed trace under ARANDU_E2E_TRACE_DIR, not the passing one.
func TestExplainFindsTheLatestFailedTrace(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ARANDU_E2E_TRACE_DIR", base)
	failedTrace(t, filepath.Join(base, "TestInvoiceIsPaid"))
	passing := e2etrace.NewRecorder("TestHome")
	if err := passing.Write(filepath.Join(base, "TestHome"), false); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exercise(t, "e2e:explain")
	if code != 0 || !strings.Contains(stdout, "TestInvoiceIsPaid") {
		t.Errorf("e2e:explain did not read the failed trace: %s %s", stdout, stderr)
	}
	if code, _, stderr := exercise(t, "e2e:explain", t.TempDir()); code == 0 || !strings.Contains(stderr, "there is no trace under") {
		t.Errorf("an empty directory was not refused: %s", stderr)
	}
}
