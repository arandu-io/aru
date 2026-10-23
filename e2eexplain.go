package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	e2etrace "github.com/arandu-io/hesape/arandutest/trace"
)

// e2eExplainUsage is the usage line of e2e:explain, in the dispatch table and
// in its refusal.
const e2eExplainUsage = "aru e2e:explain [trace-dir] [--json]"

// e2eExplainHelp is what `aru e2e:explain --help` prints under the usage line.
const e2eExplainHelp = `It reads the trace a failing journey wrote -- trace.json beside the PNG and
the DOM of every step -- and says what most probably broke it, before the
details: a response of 500 or more with what the server logged, the SQL it ran
and the policy decisions it took for that request; a policy that refused; an
exception or a console error in the page; a request that never got an answer;
and last, the step whose expectation did not hold.

Without a directory it reads the most recent failed trace under
ARANDU_E2E_TRACE_DIR, or under arandu-e2e in the system's temporary directory,
which is where a journey writes it. A directory that holds no trace.json is
searched the same way. --json prints the explanation as JSON.`

// explanation is what e2e:explain says about one trace, in the order a person
// reads it: the cause first, then what supports it.
type explanation struct {
	Dir     string `json:"dir"`
	Test    string `json:"test"`
	Driver  string `json:"driver,omitempty"`
	Browser string `json:"browser,omitempty"`
	Failed  bool   `json:"failed"`

	// Cause is the probable cause in one sentence, and Evidence the lines of
	// the trace it was read from.
	Cause    string   `json:"cause"`
	Evidence []string `json:"evidence,omitempty"`

	FailingStep *e2etrace.Step `json:"failingStep,omitempty"`
	// LastStep is the last step recorded, when no step failed.
	LastStep *e2etrace.Step `json:"lastStep,omitempty"`

	Failures      []e2etrace.Failure `json:"failures,omitempty"`
	ServerErrors  []serverAnswer     `json:"serverErrors,omitempty"`
	NetworkFailed []e2etrace.Request `json:"networkFailed,omitempty"`
	ConsoleErrors []e2etrace.Message `json:"consoleErrors,omitempty"`
	Refusals      []serverAnswer     `json:"refusals,omitempty"`
}

// serverAnswer is one request the page made, with what the server did for it.
type serverAnswer struct {
	Request e2etrace.Request `json:"request"`
	Server  *e2etrace.Server `json:"server,omitempty"`
}

// e2eExplain is the command.
func e2eExplain(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("e2e:explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the explanation as JSON")
	dir, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("e2e:explain: %w", err)
	}
	if dir == "" && fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: %s", e2eExplainUsage)
	}

	found, err := findTrace(dir)
	if err != nil {
		return err
	}
	t, err := e2etrace.Read(found)
	if err != nil {
		return fmt.Errorf("e2e:explain: %w", err)
	}
	e := explainTrace(t, found)
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(e)
	}
	writeExplanation(stdout, e)
	return nil
}

// traceBase is where journeys write their traces.
func traceBase() string {
	if base := os.Getenv("ARANDU_E2E_TRACE_DIR"); base != "" {
		return base
	}
	return filepath.Join(os.TempDir(), "arandu-e2e")
}

// findTrace answers the trace directory to read: dir itself when it holds a
// trace.json, and otherwise the most recent failed trace under it -- or under
// the trace base when dir is empty. A failed trace is preferred to a passing
// one, because the question this command answers is why something failed.
func findTrace(dir string) (string, error) {
	if dir == "" {
		dir = traceBase()
	}
	if _, err := os.Stat(filepath.Join(dir, e2etrace.FileName)); err == nil {
		return dir, nil
	}
	if !isDir(dir) {
		return "", fmt.Errorf("e2e:explain: %s is not a trace directory: a journey writes its trace, a %s and a "+
			"steps directory, when it fails, and the failure names the directory", dir, e2etrace.FileName)
	}
	type candidate struct {
		dir      string
		failed   bool
		finished time.Time
	}
	var all []candidate
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != e2etrace.FileName {
			return nil
		}
		t, err := e2etrace.Read(filepath.Dir(path))
		if err != nil {
			return nil
		}
		all = append(all, candidate{filepath.Dir(path), t.Failed, t.Finished})
		return nil
	})
	if len(all) == 0 {
		return "", fmt.Errorf("e2e:explain: there is no trace under %s. A journey writes one when it fails, or after "+
			"every test with ARANDU_E2E_TRACE=1", dir)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].failed != all[j].failed {
			return all[i].failed
		}
		return all[i].finished.After(all[j].finished)
	})
	return all[0].dir, nil
}

// explainTrace reads the probable cause out of a trace.
//
// The order is the order of causation. A 5xx is why a text never appeared, so
// it comes before the expectation that waited for the text; a policy refusal
// is why a page answered 403; an exception is why a button did nothing; a
// request that got no answer is why a swap never happened. The failing step
// is the cause only when nothing under it explains it.
func explainTrace(t *e2etrace.Trace, dir string) explanation {
	e := explanation{Dir: dir, Test: t.Test, Driver: t.Driver, Browser: t.Browser, Failed: t.Failed, Failures: t.Failures}
	for i := range t.Steps {
		if t.Steps[i].Error != "" {
			step := t.Steps[i]
			e.FailingStep = &step
			break
		}
	}
	if e.FailingStep == nil && len(t.Steps) > 0 {
		step := t.Steps[len(t.Steps)-1]
		e.LastStep = &step
	}
	for _, r := range t.Network {
		answer := serverAnswer{Request: r, Server: t.Server[r.ServerRequestID]}
		switch {
		case r.Status >= 500:
			e.ServerErrors = append(e.ServerErrors, answer)
		case r.Error != "" && !strings.Contains(r.Error, "ERR_ABORTED"):
			e.NetworkFailed = append(e.NetworkFailed, r)
		}
		if answer.Server != nil {
			for _, d := range answer.Server.Decisions {
				if !d.Allowed {
					e.Refusals = append(e.Refusals, answer)
					break
				}
			}
		}
	}
	for _, m := range t.Console {
		if m.Level == "error" {
			e.ConsoleErrors = append(e.ConsoleErrors, m)
		}
	}

	switch {
	case len(e.ServerErrors) > 0:
		first := e.ServerErrors[0]
		e.Cause = fmt.Sprintf("the server answered %d to %s %s", first.Request.Status, first.Request.Method, first.Request.URL)
		e.Evidence = serverLines(first.Server, true)
		if len(e.Evidence) == 0 && first.Request.ServerRequestID == "" {
			e.Evidence = append(e.Evidence, "the response carried no X-Request-ID, so the server's side of it is not in the trace")
		}
	case len(e.Refusals) > 0:
		first := e.Refusals[0]
		for _, d := range first.Server.Decisions {
			if d.Allowed {
				continue
			}
			e.Cause = fmt.Sprintf("%s refused %s to %s %s", d.Policy, d.Action, first.Request.Method, first.Request.URL)
			if d.Reason != "" {
				e.Cause += ": " + d.Reason
			}
			break
		}
		e.Evidence = serverLines(first.Server, true)
	case hasFailure(t.Failures, "exception"):
		f := firstFailure(t.Failures, "exception")
		e.Cause = "the page threw an exception it did not catch: " + f.Text
	case hasFailure(t.Failures, "csp"):
		e.Cause = "the Content-Security-Policy blocked something the page needed: " + firstFailure(t.Failures, "csp").Text
	case len(e.ConsoleErrors) > 0:
		e.Cause = "the page wrote an error to the console: " + e.ConsoleErrors[0].Text
	case len(e.NetworkFailed) > 0:
		r := e.NetworkFailed[0]
		e.Cause = fmt.Sprintf("%s %s got no answer: %s", r.Method, r.URL, r.Error)
	case len(t.Failures) > 0:
		f := t.Failures[0]
		e.Cause = f.Kind + ": " + f.Text
	case e.FailingStep != nil:
		s := e.FailingStep
		e.Cause = fmt.Sprintf("step %d, %s, did not hold: %s", s.Index, strings.TrimSpace(s.Action+" "+s.Target), s.Error)
	case t.Failed:
		e.Cause = "the test failed on an assertion of its own, after the browser's last step; the page itself reported nothing"
	default:
		e.Cause = "the test passed: this trace was written because ARANDU_E2E_TRACE asked for it"
	}
	return e
}

func hasFailure(list []e2etrace.Failure, kind string) bool {
	_, ok := findFailure(list, kind)
	return ok
}

func firstFailure(list []e2etrace.Failure, kind string) e2etrace.Failure {
	f, _ := findFailure(list, kind)
	return f
}

func findFailure(list []e2etrace.Failure, kind string) (e2etrace.Failure, bool) {
	for _, f := range list {
		if strings.Contains(strings.ToLower(f.Kind), kind) {
			return f, true
		}
	}
	return e2etrace.Failure{}, false
}

// serverLines renders the server's side of one request: the log lines, the
// statements and the policy decisions. With problemsFirst, the error and
// warning lines, the failed statements and the refusals come before the rest.
func serverLines(s *e2etrace.Server, problemsFirst bool) []string {
	if s == nil {
		return nil
	}
	var problems, rest []string
	for _, l := range s.Logs {
		line := fmt.Sprintf("log %s %s", strings.ToUpper(l.Level), l.Message)
		if len(l.Attrs) > 0 {
			keys := make([]string, 0, len(l.Attrs))
			for k := range l.Attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				line += fmt.Sprintf(" %s=%v", k, l.Attrs[k])
			}
		}
		if lvl := strings.ToLower(l.Level); lvl == "error" || lvl == "warn" || lvl == "warning" {
			problems = append(problems, line)
		} else {
			rest = append(rest, line)
		}
	}
	for _, q := range s.Queries {
		line := "sql " + strings.Join(strings.Fields(q.SQL), " ")
		if q.Error != "" {
			problems = append(problems, line+" -- error: "+q.Error)
		} else {
			rest = append(rest, line)
		}
	}
	for _, d := range s.Decisions {
		if d.Allowed {
			line := fmt.Sprintf("policy %s allowed %s", d.Policy, d.Action)
			if d.Grant != "" {
				line += " (" + d.Grant + ")"
			}
			rest = append(rest, line)
			continue
		}
		line := fmt.Sprintf("policy %s refused %s", d.Policy, d.Action)
		if d.Reason != "" {
			line += ": " + d.Reason
		}
		problems = append(problems, line)
	}
	if !problemsFirst {
		return append(rest, problems...)
	}
	return append(problems, rest...)
}

// writeExplanation prints an explanation for a person.
func writeExplanation(w io.Writer, e explanation) {
	status := "passed"
	if e.Failed {
		status = "failed"
	}
	engine := strings.Trim(strings.Join([]string{e.Driver, e.Browser}, ", "), ", ")
	if engine != "" {
		engine = " (" + engine + ")"
	}
	fmt.Fprintf(w, "%s%s %s\ntrace: %s\n\n", e.Test, engine, status, e.Dir)
	fmt.Fprintf(w, "Probable cause: %s\n", e.Cause)
	for _, line := range e.Evidence {
		fmt.Fprintf(w, "  %s\n", line)
	}

	step := e.FailingStep
	heading := "Failing step"
	if step == nil {
		step, heading = e.LastStep, "Last step"
	}
	if step != nil {
		fmt.Fprintf(w, "\n%s: %d %s\n", heading, step.Index, strings.TrimSpace(step.Action+" "+step.Target))
		if step.URL != "" {
			fmt.Fprintf(w, "  at %s\n", step.URL)
		}
		if step.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", step.Error)
		}
		if step.Screenshot != "" {
			fmt.Fprintf(w, "  screenshot: %s\n", filepath.Join(e.Dir, filepath.FromSlash(step.Screenshot)))
		}
		if step.DOM != "" {
			fmt.Fprintf(w, "  dom: %s\n", filepath.Join(e.Dir, filepath.FromSlash(step.DOM)))
		}
	}

	if len(e.Failures) > 0 {
		fmt.Fprintln(w, "\nWhat the page reported:")
		for _, f := range e.Failures {
			fmt.Fprintf(w, "  %s %s: %s\n", f.Time.Format("15:04:05.000"), f.Kind, f.Text)
		}
	}
	if len(e.ServerErrors) > 0 || len(e.Refusals) > 0 {
		fmt.Fprintln(w, "\nServer:")
		seen := map[string]bool{}
		for _, a := range append(append([]serverAnswer(nil), e.ServerErrors...), e.Refusals...) {
			key := a.Request.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			fmt.Fprintf(w, "  %s %s -> %d", a.Request.Method, a.Request.URL, a.Request.Status)
			if a.Request.ServerRequestID != "" {
				fmt.Fprintf(w, " (request %s)", a.Request.ServerRequestID)
			}
			fmt.Fprintln(w)
			for _, line := range serverLines(a.Server, true) {
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
	}
	if len(e.NetworkFailed) > 0 {
		fmt.Fprintln(w, "\nRequests with no answer:")
		for _, r := range e.NetworkFailed {
			fmt.Fprintf(w, "  %s %s: %s\n", r.Method, r.URL, r.Error)
		}
	}
	if len(e.ConsoleErrors) > 0 {
		fmt.Fprintln(w, "\nConsole errors:")
		for _, m := range e.ConsoleErrors {
			fmt.Fprintf(w, "  %s\n", m.Text)
		}
	}
}
