package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/arandutest/browser"
	"github.com/arandu-io/hesape/arandutest/browser/cdp"
)

// headless launches Chrome without a window for a test, skipping on a machine
// with no browser and failing in CI, where a skipped browser test is one that
// never runs.
func headless(t *testing.T) browser.Driver {
	t.Helper()
	d, err := cdp.Launch(t.Context(), browser.Options{Browser: "chromium"})
	if err != nil {
		if errors.Is(err, browser.ErrNoBrowser) && os.Getenv("CI") == "" {
			t.Skipf("%v", err)
		}
		t.Fatalf("launching Chrome: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// invoiceSite is a small application with a list, a page per invoice and a
// form that pays it.
func invoiceSite() http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Invoices</title></head><body><main>%s</main></body></html>`, body)
	}
	mux.HandleFunc("GET /invoices", func(w http.ResponseWriter, r *http.Request) {
		page(w, `<h1>Invoices</h1><a href="/invoices/7">Invoice 7</a>`)
	})
	mux.HandleFunc("GET /invoices/{id}", func(w http.ResponseWriter, r *http.Request) {
		page(w, `<h1>Invoice 7</h1><form method="post" action="/invoices/7/pay">
<label for="card">Card number</label><input id="card" name="card">
<label><input type="checkbox" name="remember"> Remember me</label>
<button type="submit">Confirm</button></form>`)
	})
	mux.HandleFunc("POST /invoices/{id}/pay", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/invoices/7?paid=1", http.StatusSeeOther)
	})
	return mux
}

// centre answers the middle of the element the expression finds, in viewport
// pixels, for a pointer to press it.
func centre(t *testing.T, d browser.Driver, find string) (float64, float64) {
	t.Helper()
	var at struct{ X, Y float64 }
	expr := `(() => { const el = ` + find + `; el.scrollIntoView({block: 'center'}); const r = el.getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`
	deadline := time.After(10 * time.Second)
	retry := time.NewTicker(50 * time.Millisecond)
	defer retry.Stop()
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		err := d.Evaluate(ctx, expr, &at)
		cancel()
		if err == nil {
			return at.X, at.Y
		}
		select {
		case <-retry.C:
		case <-deadline:
			t.Fatalf("finding %s: %v", find, err)
		}
	}
}

// waitFor waits until the recording holds an event that satisfies ok.
func waitFor(t *testing.T, rec *recording, what string, ok func(recordedEvent) bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		for _, e := range rec.snapshot() {
			if ok(e) {
				return
			}
		}
		select {
		case <-rec.changed:
		case <-deadline:
			t.Fatalf("the recorder never reported %s; it reported %+v", what, rec.snapshot())
		}
	}
}

// TestTheRecorderWritesTheJourneyAPersonWalked drives a headless Chrome with
// the pointer and the keyboard -- the input a person makes -- while the
// recorder runs, and reads the journey it writes: route names for the pages a
// route answers, a label for the field, the role and name of the link and the
// button, an ExpectURL for every page an action led to, and a path for the
// address no route matches.
func TestTheRecorderWritesTheJourneyAPersonWalked(t *testing.T) {
	site := httptest.NewServer(invoiceSite())
	t.Cleanup(site.Close)
	d := &serialDriver{Driver: headless(t)}

	rec := newRecording()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- record(t.Context(), d, site.URL+"/invoices", rec, stop) }()

	waitFor(t, rec, "the first page", func(e recordedEvent) bool { return e.Kind == "load" })
	press := func(find string) {
		x, y := centre(t, d, find)
		if err := d.Click(t.Context(), x, y); err != nil {
			t.Fatal(err)
		}
	}
	press(`document.querySelector('a')`)
	waitFor(t, rec, "the invoice page", func(e recordedEvent) bool { return e.Kind == "load" && strings.HasSuffix(e.URL, "/invoices/7") })
	press(`document.querySelector('input[type=checkbox]')`)
	press(`document.getElementById('card')`)
	if err := d.InsertText(t.Context(), "4242"); err != nil {
		t.Fatal(err)
	}
	press(`document.querySelector('button')`)
	waitFor(t, rec, "the paid page", func(e recordedEvent) bool { return e.Kind == "load" && strings.Contains(e.URL, "paid=1") })
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	base, _ := url.Parse(site.URL)
	routes := []namedRoute{{"invoices.index", "/invoices"}, {"invoices.show", "/invoices/{invoice}"}}
	got := recordedSteps(rec.snapshot(), routes, base)
	want := []string{
		`page.Visit("invoices.index")`,
		`page.ClickRole("link", "Invoice 7")`,
		`page.ExpectURL("invoices.show", "7")`,
		`page.Check("Remember me")`,
		`page.Fill("Card number", "4242")`,
		`page.ClickRole("button", "Confirm")`,
		`page.ExpectURL("/invoices/7?paid=1")`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the recorded journey is\n%s\n\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	root := t.TempDir()
	j := journey{Name: "PayAnInvoice", ModulePath: "example.test/shop"}
	if err := writeRecording(root, j, rec.snapshot(), routes, base, false, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "tests", "E2E", "PayAnInvoice_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range append(want, "//go:build e2e", "package e2e_test", `"example.test/shop/tests"`,
		"func TestPayAnInvoice(t *testing.T) {", "server := tests.Serve(t)", "page := tests.Open(t, server)") {
		if !strings.Contains(string(body), line) {
			t.Errorf("the journey file does not carry %q:\n%s", line, body)
		}
	}
	if strings.Contains(string(body), "github.com/arandu-io/framework/test/browser") {
		t.Errorf("the journey imports the browser package and names nothing in it:\n%s", body)
	}
}

// TestRecordedStepsSpeakTheKitsWords turns events the browser cannot be made
// to produce headless on every platform -- a native select, a password, an
// element with nothing but a CSS path -- into steps.
func TestRecordedStepsSpeakTheKitsWords(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:8080")
	got := recordedSteps([]recordedEvent{
		{Kind: "load", URL: "http://127.0.0.1:8080/settings"},
		{Kind: "select", Loc: recordedLocator{Kind: "label", A: "Country"}, Value: "Brazil"},
		{Kind: "fill", Loc: recordedLocator{Kind: "label", A: "Password"}, Value: "hunter2", Secret: true},
		{Kind: "fill", Loc: recordedLocator{Kind: "role", A: "textbox", B: "Search"}, Value: "ada"},
		{Kind: "uncheck", Loc: recordedLocator{Kind: "testid", A: "news"}},
		{Kind: "click", Loc: recordedLocator{Kind: "css", A: "main > div:nth-of-type(2)"}},
		{Kind: "submit"},
		{Kind: "press", Value: "Escape"},
		{Kind: "load", URL: "https://elsewhere.test/"},
	}, nil, base)
	want := []string{
		`page.Visit("/settings")`,
		`page.Select("Country", "Brazil")`,
		`// The password typed while recording is not written down: put the fixture's here.`,
		`page.Fill("Password", "password")`,
		`page.FillIn(browser.Role("textbox", "Search"), "ada")`,
		`page.CheckIn(browser.TestID("news"), false)`,
		`page.Click(browser.CSS("main > div:nth-of-type(2)", "the recorder found no role, label, text or test id for it"))`,
		`page.Submit()`,
		`page.Press("Escape")`,
		`page.ExpectURL("https://elsewhere.test/")`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps are\n%s\n\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(strings.Join(got, "\n"), "hunter2") {
		t.Error("the typed password was written into the journey")
	}
}

// TestRecordRefusesWhatItCannotWrite runs the command where the journey could
// not compile -- tests/ declares no Serve and Open -- and where the address is
// neither a URL, a path nor a route, and requires both refused before a
// browser opens.
func TestRecordRefusesWhatItCannotWrite(t *testing.T) {
	root := probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	code, _, stderr := exercise(t, "e2e:record")
	if code == 0 || !strings.Contains(stderr, "var Drivers = browser.Registry") || !strings.Contains(stderr, "github.com/arandu-io/framework/test/browser/cdp") {
		t.Errorf("a project without the journey helpers was not told what to paste: %s", stderr)
	}
	writeFile(t, filepath.Join(root, "tests", "TestCase.go"), "package tests\n\nfunc Serve() {}\n\nfunc Open() {}\n")
	if code, _, stderr := exercise(t, "e2e:record", "no.such.route"); code == 0 || !strings.Contains(stderr, "neither an address") {
		t.Errorf("an unknown route was not refused: %s", stderr)
	}
	if code, _, stderr := exercise(t, "e2e:record", "/", "--base=http://127.0.0.1:1"); code == 0 || !strings.Contains(stderr, "nothing answers") {
		t.Errorf("an application that is not running was not reported: %s", stderr)
	}
}

// TestRouteTargetsNameTheRoute matches addresses against route patterns.
func TestRouteTargetsNameTheRoute(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:8080")
	routes := []namedRoute{{"invoices.create", "/invoices/new"}, {"files.show", "/files/{path...}"}, {"invoices.show", "/invoices/{invoice}"}, {"home", "/"}}
	for raw, want := range map[string]string{
		"http://127.0.0.1:8080/":               `"home"`,
		"http://127.0.0.1:8080/invoices/new":   `"invoices.create"`,
		"http://127.0.0.1:8080/invoices/a%20b": `"invoices.show", "a b"`,
		"http://127.0.0.1:8080/files/a/b.txt":  `"files.show", "a/b.txt"`,
		"http://127.0.0.1:8080/nowhere/x":      `"/nowhere/x"`,
		"http://127.0.0.1:8080/invoices?q=1":   `"/invoices?q=1"`,
	} {
		if got := strings.Join(routeTarget(routes, base, raw), ", "); got != want {
			t.Errorf("routeTarget(%s) = %s, want %s", raw, got, want)
		}
	}
	if journeyName("sign-up flow") != "SignUpFlow" || journeyName("TestCheckout") != "Checkout" || journeyName("Testimonials") != "Testimonials" || journeyName("") != "Journey" {
		t.Errorf("journeyName does not write exported names: %q %q %q", journeyName("sign-up flow"), journeyName("TestCheckout"), journeyName(""))
	}
}
