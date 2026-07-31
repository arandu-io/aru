package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// trace reconstructs one request in the terminal.
//
// It reads the running application's console rather than the process memory it
// cannot see: a CLI is a separate process, and the ring buffer lives in the
// server. So this is an HTTP client for /_arandu/debug, which means the server
// has to be up -- which it is, because you are looking at a request it just
// handled.
//
// One source for the page and the terminal. A second endpoint for the CLI would
// be a second thing to keep in sync with what the console shows.
func trace(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("trace", flag.ContinueOnError)
	flags.SetOutput(stderr)
	host := flags.String("host", defaultHost(), "where the application is listening")
	secret := flags.String("secret", os.Getenv("ARANDU_TRACING_SECRET"), "the X-Arandu-Trace secret, for tracing outside development")
	asJSON := flags.Bool("json", false, "print the raw response instead of the report")

	// The id comes first and the flags after it, which is how everyone types it
	// and what the flag package does not support.
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	if id == "" && flags.NArg() > 0 {
		id = flags.Arg(0)
	}

	body, err := fetchTrace(*host, id, *secret)
	if err != nil {
		return err
	}

	if *asJSON {
		fmt.Fprintln(stdout, string(body))
		return nil
	}
	if id == "" {
		return printList(stdout, body)
	}
	return printRequest(stdout, body)
}

// defaultHost is where `aru serve` listens unless told otherwise.
func defaultHost() string {
	if v := os.Getenv("APP_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port
}

func fetchTrace(host, id, secret string) ([]byte, error) {
	url := strings.TrimSuffix(host, "/") + "/_arandu/debug"
	if id != "" {
		url += "/" + id
	}
	url += "?format=json"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	if secret != "" {
		req.Header.Set("X-Arandu-Trace", secret)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			// The most common failure by far, and the message from net/http
			// names a port and a syscall rather than the thing to do about it.
			return nil, fmt.Errorf("nothing is listening on %s.\nStart the application with `aru dev`, or point at it with --host", host)
		}
		return nil, fmt.Errorf("trace: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("trace: reading the response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		// 404 from the console means "no such request"; 404 from the router
		// means the console is not mounted, which is a different problem with
		// the same status code.
		if strings.Contains(string(body), "\"error\"") {
			return nil, fmt.Errorf("no request with id %s.\nThe buffer keeps the most recent ones and drops the rest -- reproduce it and try again", id)
		}
		return nil, fmt.Errorf("%s has no debug console.\nIt is mounted in development, and in other environments only when APP_TRACING_SECRET is set", host)
	default:
		return nil, fmt.Errorf("trace: %s answered %s", host, resp.Status)
	}
}

// listPayload and requestPayload mirror what the console encodes. They are
// declared here rather than shared with the framework on purpose: the CLI reads
// the console over HTTP like any other client, and a shared type would make the
// two versions have to match.
type listPayload struct {
	Rows []struct {
		ID       string `json:"ID"`
		Method   string `json:"Method"`
		Path     string `json:"Path"`
		Status   int    `json:"Status"`
		Duration string `json:"Duration"`
		At       string `json:"At"`
		Queries  int    `json:"Queries"`
		SQL      string `json:"SQL"`
		NPlusOne bool   `json:"NPlusOne"`
		Slow     bool   `json:"Slow"`
	} `json:"Rows"`
}

type requestPayload struct {
	ID       string   `json:"ID"`
	Method   string   `json:"Method"`
	Path     string   `json:"Path"`
	Status   int      `json:"Status"`
	Duration string   `json:"Duration"`
	At       string   `json:"At"`
	Findings []string `json:"Findings"`
	Timeline []struct {
		Name    string `json:"Name"`
		Value   string `json:"Value"`
		Percent int    `json:"Percent"`
	} `json:"Timeline"`
	Queries []struct {
		SQL      string `json:"SQL"`
		Args     string `json:"Args"`
		Duration string `json:"Duration"`
		Rows     int    `json:"Rows"`
		Repeated int    `json:"Repeated"`
		Origin   string `json:"Origin"`
		Error    string `json:"Error"`
	} `json:"Queries"`
	Dumps []struct {
		Label  string `json:"Label"`
		Value  string `json:"Value"`
		At     string `json:"At"`
		Origin string `json:"Origin"`
	} `json:"Dumps"`
	External []struct {
		Method   string `json:"Method"`
		URL      string `json:"URL"`
		Status   int    `json:"Status"`
		Duration string `json:"Duration"`
	} `json:"External"`
	Events []struct {
		Name    string `json:"Name"`
		Payload string `json:"Payload"`
		At      string `json:"At"`
	} `json:"Events"`
}

func printList(w io.Writer, body []byte) error {
	var payload listPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("trace: the console answered something unexpected: %w", err)
	}

	if len(payload.Rows) == 0 {
		fmt.Fprintln(w, "nothing recorded yet. Make a request and try again.")
		return nil
	}

	fmt.Fprintf(w, "%-18s  %-6s  %-32s  %6s  %9s  %7s  %s\n",
		"id", "method", "path", "status", "duration", "queries", "")
	for _, r := range payload.Rows {
		flags := ""
		if r.NPlusOne {
			flags += " N+1"
		}
		if r.Slow {
			flags += " slow"
		}
		fmt.Fprintf(w, "%-18s  %-6s  %-32s  %6d  %9s  %7d %s\n",
			r.ID, r.Method, truncate(r.Path, 32), r.Status, r.Duration, r.Queries, flags)
	}
	fmt.Fprintf(w, "\naru trace <id> for one of them.\n")
	return nil
}

func printRequest(w io.Writer, body []byte) error {
	var p requestPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("trace: the console answered something unexpected: %w", err)
	}

	fmt.Fprintf(w, "%s %s  %d  %s  (%s)\n%s\n\n", p.Method, p.Path, p.Status, p.Duration, p.At, p.ID)

	// The diagnosis first. It is the reason to run this command.
	if len(p.Findings) > 0 {
		for _, f := range p.Findings {
			fmt.Fprintf(w, "  ! %s\n", f)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "timeline")
	for _, t := range p.Timeline {
		fmt.Fprintf(w, "  %-9s %8s  %3d%%  %s\n", t.Name, t.Value, t.Percent, bar(t.Percent))
	}

	if len(p.Queries) > 0 {
		fmt.Fprintf(w, "\nqueries (%d)\n", len(p.Queries))
		for _, q := range p.Queries {
			repeated := ""
			if q.Repeated > 1 {
				repeated = fmt.Sprintf("  x%d", q.Repeated)
			}
			fmt.Fprintf(w, "  %8s  %4d rows  %s%s\n", q.Duration, q.Rows, q.Origin, repeated)
			fmt.Fprintf(w, "            %s\n", truncate(q.SQL, 96))
			if q.Args != "" {
				fmt.Fprintf(w, "            args: %s\n", truncate(q.Args, 96))
			}
			if q.Error != "" {
				fmt.Fprintf(w, "            error: %s\n", q.Error)
			}
		}
	}

	if len(p.Dumps) > 0 {
		fmt.Fprintf(w, "\ndumps (%d)\n", len(p.Dumps))
		for _, d := range p.Dumps {
			fmt.Fprintf(w, "  %s  at %s  %s\n", d.Label, d.At, d.Origin)
			for _, line := range strings.Split(d.Value, "\n") {
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
	}

	if len(p.External) > 0 {
		fmt.Fprintf(w, "\noutbound calls (%d)\n", len(p.External))
		for _, x := range p.External {
			fmt.Fprintf(w, "  %8s  %3d  %s %s\n", x.Duration, x.Status, x.Method, truncate(x.URL, 80))
		}
	}

	if len(p.Events) > 0 {
		fmt.Fprintf(w, "\nevents (%d)\n", len(p.Events))
		for _, e := range p.Events {
			fmt.Fprintf(w, "  %s  at %s\n", e.Name, e.At)
		}
	}
	return nil
}

// bar draws the proportion, so the timeline is readable without doing the
// arithmetic. Twenty cells: each one is five percent.
func bar(percent int) string {
	filled := percent / 5
	if filled > 20 {
		filled = 20
	}
	return strings.Repeat("#", filled) + strings.Repeat(".", 20-filled)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
