package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/hesape/arandutest/browser"
	"github.com/arandu-io/hesape/arandutest/browser/cdp"
	e2etrace "github.com/arandu-io/hesape/arandutest/trace"
	"github.com/arandu-io/mcp"
)

// e2eSession is the one browser page the assistant drives, shared by the
// browser tools of one aru mcp. It is the test kit's page over the native
// driver, so what the assistant sees -- the waiting, the locators, a 500 or a
// console error failing the step -- is what a journey in tests/E2E sees, and
// what it finds can be written down as one.
type e2eSession struct {
	root string
	// launch starts the browser. A field so a test can launch it headless.
	launch func(ctx context.Context, headed bool) (browser.Driver, error)

	mu       sync.Mutex
	t        *mcpT
	page     *browser.Page
	driver   browser.Driver
	captures int
}

func newE2ESession(root string) *e2eSession {
	return &e2eSession{root: root, launch: func(ctx context.Context, headed bool) (browser.Driver, error) {
		return cdp.Launch(ctx, browser.Options{Browser: "chromium", Headed: headed})
	}}
}

// mcpT is what the kit's page reports to in place of a test. A step that
// fails stops the step as Fatalf stops a test: by unwinding to the tool,
// which answers the failure to the assistant and keeps the page open.
type mcpT struct {
	testing.TB // never called: the page calls only the methods below

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	failed   bool
	errs     []string
	cleanups []func()
}

// stepFailed is what Fatalf unwinds with.
type stepFailed struct{ message string }

func newMCPT() *mcpT {
	ctx, cancel := context.WithCancel(context.Background())
	return &mcpT{ctx: ctx, cancel: cancel}
}

func (t *mcpT) Helper()                  {}
func (t *mcpT) Name() string             { return "aru-mcp" }
func (t *mcpT) Context() context.Context { return t.ctx }
func (t *mcpT) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}
func (t *mcpT) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed
}
func (t *mcpT) Fail() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
}
func (t *mcpT) Skipped() bool            { return false }
func (t *mcpT) Log(args ...any)          {}
func (t *mcpT) Logf(string, ...any)      {}
func (t *mcpT) Error(args ...any)        { t.Errorf("%s", fmt.Sprint(args...)) }
func (t *mcpT) Fatal(args ...any)        { t.Fatalf("%s", fmt.Sprint(args...)) }
func (t *mcpT) FailNow()                 { t.Fatalf("the step failed") }
func (t *mcpT) Skip(args ...any)         { t.Fatal(args...) }
func (t *mcpT) SkipNow()                 { t.FailNow() }
func (t *mcpT) Skipf(f string, a ...any) { t.Fatalf(f, a...) }
func (t *mcpT) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
	t.errs = append(t.errs, fmt.Sprintf(format, args...))
}
func (t *mcpT) Fatalf(format string, args ...any) {
	t.Fail()
	panic(stepFailed{fmt.Sprintf(format, args...)})
}

// drain returns the errors reported since the last call.
func (t *mcpT) drain() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.errs
	t.errs = nil
	return out
}

// step runs fn on the page and answers the failure it reported, if any.
func (s *e2eSession) step(fn func(p *browser.Page)) (failure string) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(stepFailed)
			if !ok {
				panic(r)
			}
			failure = f.message
		}
		if errs := s.t.drain(); len(errs) > 0 {
			failure = strings.TrimSpace(failure + "\n" + strings.Join(errs, "\n"))
		}
	}()
	fn(s.page)
	return ""
}

// open starts the browser and the page when none is open.
func (s *e2eSession) open(ctx context.Context, base string, headed bool) error {
	if s.page != nil {
		return nil
	}
	d, err := s.launch(ctx, headed)
	if err != nil {
		return err
	}
	s.t, s.driver = newMCPT(), d
	routes := projectRoutes(s.root)
	resolve := func(name string, params ...any) (string, error) {
		for _, r := range routes {
			if r.Name == name {
				return fillPattern(r.Pattern, params)
			}
		}
		return "", fmt.Errorf("this project has no GET route named %s", name)
	}
	failure := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				f, ok := r.(stepFailed)
				if !ok {
					panic(r)
				}
				failure = f.message
			}
		}()
		s.page = browser.New(s.t, d, browser.BaseURL(base), browser.Routes(resolve), browser.Timeout(10*time.Second))
	}()
	if failure != "" {
		s.closeLocked()
		return errors.New(failure)
	}
	return nil
}

// fillPattern puts the parameters into a route pattern in order.
func fillPattern(pattern string, params []any) (string, error) {
	i := 0
	var missing bool
	out := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllStringFunc(pattern, func(string) string {
		if i >= len(params) {
			missing = true
			return ""
		}
		v := fmt.Sprint(params[i])
		i++
		return v
	})
	if missing || i != len(params) {
		return "", fmt.Errorf("the route %s takes %d parameters and was given %d", pattern, strings.Count(pattern, "{"), len(params))
	}
	return out, nil
}

// close ends the page and the browser.
func (s *e2eSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *e2eSession) closeLocked() {
	if s.t != nil {
		for i := len(s.t.cleanups) - 1; i >= 0; i-- {
			func() {
				defer func() { _ = recover() }()
				s.t.cleanups[i]()
			}()
		}
		s.t.cancel()
	}
	if s.driver != nil {
		_ = s.driver.Close()
	}
	s.t, s.page, s.driver = nil, nil, nil
}

// tree answers the accessibility tree of the page.
func (s *e2eSession) tree() (mcp.Response, error) {
	var out struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Tree  string `json:"tree"`
	}
	if failure := s.step(func(p *browser.Page) { p.Evaluate(accessibilityTree, &out) }); failure != "" {
		return s.failed(failure), nil
	}
	return mcp.Text("%s\n%s\n\n%s", out.Title, out.URL, out.Tree), nil
}

// failed answers a failed step, with where the trace can be read.
func (s *e2eSession) failed(failure string) mcp.Response {
	return mcp.Error("%s\n\nThe page stays open. e2e_read_trace explains the failure with the server's side; a defect "+
		"found this way is written down as a journey in tests/E2E (aru make:e2e, or aru e2e:record).", failure)
}

// noPage is the answer of a tool that needs a page before e2e_open.
func noPage() mcp.Response {
	return mcp.Error("no page is open: call e2e_open first, with the address, path or route name to visit")
}

// locatorFrom reads the locator a tool was called with: role and name,
// label, text or test id, in that order.
func locatorFrom(r mcp.Request) (browser.Locator, bool) {
	role, _ := r.String("role")
	name, _ := r.String("name")
	label, _ := r.String("label")
	text, _ := r.String("text")
	testid, _ := r.String("testid")
	switch {
	case role != "":
		return browser.Role(role, name), true
	case label != "":
		return browser.Label(label), true
	case text != "":
		return browser.Text(text), true
	case testid != "":
		return browser.TestID(testid), true
	}
	return browser.Locator{}, false
}

// locatorFields are the arguments locatorFrom reads.
func locatorFields() []mcp.Field {
	return []mcp.Field{
		mcp.String("role", "the ARIA role of the element: button, link, textbox, checkbox, heading..."),
		mcp.String("name", "its accessible name, with role: the text a screen reader says"),
		mcp.String("label", "the text of a form control's label"),
		mcp.String("text", "the visible text of the element"),
		mcp.String("testid", "its data-testid"),
	}
}

// e2eTool is one browser tool: a name, what it does, its arguments and its
// work on the session.
type e2eTool struct {
	session     *e2eSession
	name        string
	description string
	schema      mcp.Schema
	handle      func(ctx context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error)
}

func (t e2eTool) Name() string        { return t.name }
func (t e2eTool) Description() string { return t.description }
func (t e2eTool) Schema() mcp.Schema  { return t.schema }
func (t e2eTool) Handle(ctx context.Context, r mcp.Request) (mcp.Response, error) {
	t.session.mu.Lock()
	defer t.session.mu.Unlock()
	return t.handle(ctx, t.session, r)
}

// e2eTools are the browser tools over one session.
func e2eTools(s *e2eSession) []mcp.Tool {
	needPage := func(fn func(ctx context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error)) func(context.Context, *e2eSession, mcp.Request) (mcp.Response, error) {
		return func(ctx context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
			if s.page == nil {
				return noPage(), nil
			}
			return fn(ctx, s, r)
		}
	}
	return []mcp.Tool{
		e2eTool{session: s, name: "e2e_open",
			description: "Open the running application (aru dev) in Chrome through the test kit's native driver and visit an " +
				"address: a URL, a path, or a route name with its parameters. Answers the page's accessibility tree. The " +
				"page fails a step on an uncaught exception, a console error or a response of 500 or more, as a journey in " +
				"tests/E2E does. A defect found here should end as a journey: aru make:e2e or aru e2e:record.",
			schema: mcp.Object(
				mcp.String("target", "a URL, a path such as /invoices, or a route name such as invoices.show").Required(),
				mcp.String("params", "the route's parameters, comma separated, in order"),
				mcp.String("base", "where the application listens; APP_URL or http://127.0.0.1:8080 by default"),
				mcp.Bool("headed", "show the browser window; only when the page is first opened"),
			),
			handle: func(ctx context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				target, _ := r.String("target")
				base, _ := r.String("base")
				if base == "" {
					base = defaultHost()
				}
				headed, _ := r.Bool("headed")
				if err := s.open(ctx, strings.TrimRight(base, "/"), headed); err != nil {
					return mcp.Error("opening the browser: %v", err), nil
				}
				var params []any
				if raw, _ := r.String("params"); raw != "" {
					for _, p := range strings.Split(raw, ",") {
						params = append(params, strings.TrimSpace(p))
					}
				}
				if failure := s.step(func(p *browser.Page) { p.Visit(target, params...) }); failure != "" {
					return s.failed(failure), nil
				}
				return s.tree()
			}},
		e2eTool{session: s, name: "e2e_accessibility_tree",
			description: "Answer the open page as roles and accessible names, in document order, with heading levels, field " +
				"values and states: what the click, fill and expect tools find elements by.",
			schema: mcp.Object(),
			handle: needPage(func(_ context.Context, s *e2eSession, _ mcp.Request) (mcp.Response, error) { return s.tree() })},
		e2eTool{session: s, name: "e2e_click",
			description: "Click the one visible element the locator finds -- role and name, label, text or test id -- after " +
				"waiting for it to be visible, enabled and still, and wait for the page to settle (htmx, network, animations). " +
				"Answers the accessibility tree after the click.",
			schema: mcp.Object(locatorFields()...),
			handle: needPage(func(_ context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				l, ok := locatorFrom(r)
				if !ok {
					return mcp.Error("name the element: role and name, label, text or testid"), nil
				}
				if failure := s.step(func(p *browser.Page) { p.Click(l) }); failure != "" {
					return s.failed(failure), nil
				}
				return s.tree()
			})},
		e2eTool{session: s, name: "e2e_fill",
			description: "Type a value into the form control the locator finds, replacing what it held. A label is the usual " +
				"locator for a field. Answers the accessibility tree after.",
			schema: mcp.Object(append(locatorFields(), mcp.String("value", "what to type").Required())...),
			handle: needPage(func(_ context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				l, ok := locatorFrom(r)
				if !ok {
					return mcp.Error("name the field: label, role and name, or testid"), nil
				}
				value, _ := r.String("value")
				if failure := s.step(func(p *browser.Page) { p.FillIn(l, value) }); failure != "" {
					return s.failed(failure), nil
				}
				return s.tree()
			})},
		e2eTool{session: s, name: "e2e_expect",
			description: "Wait until the page shows something, as a journey's expectation does, and answer whether it held: " +
				"text the page reads, text it no longer reads, the address it is at (URL, path or route name), or one visible " +
				"element by role and name, label, text or test id.",
			schema: mcp.Object(append(locatorFields(),
				mcp.String("see", "text the page should read"),
				mcp.String("not_see", "text the page should no longer read"),
				mcp.String("url", "the URL, path or route name the page should be at"),
				mcp.String("params", "the route's parameters, comma separated, with url"),
			)...),
			handle: needPage(func(_ context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				see, _ := r.String("see")
				notSee, _ := r.String("not_see")
				at, _ := r.String("url")
				var params []any
				if raw, _ := r.String("params"); raw != "" {
					for _, p := range strings.Split(raw, ",") {
						params = append(params, strings.TrimSpace(p))
					}
				}
				l, located := locatorFrom(r)
				var what string
				var fn func(p *browser.Page)
				switch {
				case see != "":
					what, fn = "the page reads "+goString(see), func(p *browser.Page) { p.ExpectText(see) }
				case notSee != "":
					what, fn = "the page no longer reads "+goString(notSee), func(p *browser.Page) { p.ExpectNoText(notSee) }
				case at != "":
					what, fn = "the page is at "+at, func(p *browser.Page) { p.ExpectURL(at, params...) }
				case located:
					what, fn = l.String()+" is visible", func(p *browser.Page) { p.ExpectVisible(l) }
				default:
					return mcp.Error("say what to expect: see, not_see, url, or an element"), nil
				}
				if failure := s.step(fn); failure != "" {
					return s.failed(failure), nil
				}
				return mcp.Text("held: %s", what), nil
			})},
		e2eTool{session: s, name: "e2e_capture",
			description: "Save what the open page looks like -- a PNG of the viewport and its DOM -- to files, and answer their " +
				"paths and the address.",
			schema: mcp.Object(mcp.String("name", "a word for the files: login-error")),
			handle: needPage(func(ctx context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				name, _ := r.String("name")
				if name == "" {
					name = "capture"
				}
				var dom string
				if failure := s.step(func(p *browser.Page) { p.Evaluate(`'<!doctype html>\n' + document.documentElement.outerHTML`, &dom) }); failure != "" {
					return s.failed(failure), nil
				}
				shot, cancel := context.WithTimeout(ctx, 10*time.Second)
				png, err := s.driver.Screenshot(shot)
				cancel()
				if err != nil {
					return mcp.Error("capturing the page: %v", err), nil
				}
				s.captures++
				dir := filepath.Join(traceBase(), "aru-mcp-captures")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return mcp.Response{}, err
				}
				base := filepath.Join(dir, fmt.Sprintf("%03d-%s", s.captures, safeName(name)))
				if err := os.WriteFile(base+".png", png, 0o600); err != nil {
					return mcp.Response{}, err
				}
				if err := os.WriteFile(base+".html", []byte(dom), 0o600); err != nil {
					return mcp.Response{}, err
				}
				return mcp.Text("%s\nscreenshot: %s.png\ndom: %s.html", s.page.URL(), base, base), nil
			})},
		e2eTool{session: s, name: "e2e_read_trace",
			description: "Explain a trace: without dir, the trace of the page open here, written to disk first; with dir, " +
				"a trace a journey wrote, or the latest failed one under it. Answers the probable cause first -- a 5xx with " +
				"the server's logs, SQL and policy decisions, a refusal, an exception, a console error, the failing step -- " +
				"as aru e2e:explain does.",
			schema: mcp.Object(mcp.String("dir", "a trace directory, or a directory of traces")),
			handle: func(_ context.Context, s *e2eSession, r mcp.Request) (mcp.Response, error) {
				dir, _ := r.String("dir")
				if dir == "" && s.page != nil {
					// Where the page itself writes its trace when it closes
					// after a failure, so the two never disagree.
					dir = e2etrace.Dir(s.t.Name())
					if err := s.page.Trace().Write(dir, s.t.Failed()); err != nil {
						return mcp.Error("writing the trace: %v", err), nil
					}
				}
				found, err := findTrace(dir)
				if err != nil {
					return mcp.Error("%v", err), nil
				}
				t, err := e2etrace.Read(found)
				if err != nil {
					return mcp.Error("%v", err), nil
				}
				return jsonText(explainTrace(t, found))
			}},
		e2eTool{session: s, name: "e2e_close",
			description: "Close the page and the browser. The next e2e_open starts a new one, with no cookies.",
			schema:      mcp.Object(),
			handle: func(_ context.Context, s *e2eSession, _ mcp.Request) (mcp.Response, error) {
				if s.page == nil {
					return mcp.Text("no page was open"), nil
				}
				s.closeLocked()
				return mcp.Text("closed"), nil
			}},
	}
}

// safeName keeps letters, digits and dashes.
func safeName(s string) string {
	out := regexp.MustCompile(`[^A-Za-z0-9-]+`).ReplaceAllString(s, "-")
	if out = strings.Trim(out, "-"); out == "" {
		return "capture"
	}
	return out
}
