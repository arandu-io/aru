package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheBrowserToolsDriveThePage drives the browser tools over standard input
// and output, the way an assistant does, against a small application: open by
// path, read the tree, fill and click by label and role, expect the next page
// by its route name, capture it, and read the trace of a step that met a 500 --
// whose cause is the server's answer.
func TestTheBrowserToolsDriveThePage(t *testing.T) {
	_ = headless(t).Close() // skips with no browser, fails in CI
	mux := http.NewServeMux()
	mux.Handle("/", invoiceSite())
	mux.HandleFunc("GET /broken", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Broken</title></head><body><button onclick="fetch('/explode')">Explode</button></body></html>`)
	})
	mux.HandleFunc("GET /explode", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	site := server.URL

	root := mcpProject(t)
	t.Chdir(root)
	t.Setenv("ARANDU_E2E_TRACE_DIR", t.TempDir())
	base := `"base":"` + site + `"`
	answers, logs := converse(t, root,
		call(1, "e2e_accessibility_tree", `{}`),
		call(2, "e2e_open", `{"target":"/invoices",`+base+`}`),
		call(3, "e2e_click", `{"role":"link","name":"Invoice 7"}`),
		call(4, "e2e_expect", `{"url":"/invoices/7"}`),
		call(5, "e2e_fill", `{"label":"Card number","value":"4242"}`),
		call(6, "e2e_click", `{"role":"button","name":"Confirm"}`),
		call(7, "e2e_expect", `{"see":"Invoice 7"}`),
		call(8, "e2e_capture", `{"name":"paid"}`),
		call(9, "e2e_expect", `{"see":"Refunded"}`),
		call(10, "e2e_open", `{"target":"/broken"}`),
		call(11, "e2e_click", `{"role":"button","name":"Explode"}`),
		call(12, "e2e_read_trace", `{}`),
		call(13, "e2e_close", `{}`),
	)
	text := func(id string) string {
		a := answers[id]
		if len(a.Result.Content) == 0 {
			t.Fatalf("call %s got no answer (log: %s)", id, logs)
		}
		return a.Result.Content[0].Text
	}
	for id, c := range map[string]struct {
		failed bool
		says   string
	}{
		"1":  {true, "call e2e_open first"},
		"2":  {false, `link "Invoice 7"`},
		"3":  {false, `textbox "Card number"`},
		"4":  {false, "held: the page is at /invoices/7"},
		"5":  {false, `value="4242"`},
		"6":  {false, "Invoice 7"},
		"7":  {false, "held"},
		"8":  {false, "screenshot: "},
		"9":  {true, "Refunded"},
		"11": {true, "500"},
		"12": {false, `"cause": "the server answered 500 to GET ` + site + `/explode"`},
		"13": {false, "closed"},
	} {
		if got := text(id); answers[id].Result.IsError != c.failed || !strings.Contains(got, c.says) {
			t.Errorf("call %s: failed=%v, want %v, saying %q:\n%.800s", id, answers[id].Result.IsError, c.failed, c.says, got)
		}
	}
	shot := strings.TrimSpace(strings.SplitN(strings.SplitN(text("8"), "screenshot: ", 2)[1], "\n", 2)[0])
	if info, err := os.Stat(shot); err != nil || info.Size() == 0 || filepath.Ext(shot) != ".png" {
		t.Errorf("the capture is not a PNG on disk: %s %v", shot, err)
	}
}
