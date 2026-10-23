package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/browser/cdp"
)

// e2eRecordUsage is the usage line of e2e:record.
const e2eRecordUsage = "aru e2e:record [url-or-route] [--name=<Journey>] [--base=http://127.0.0.1:8080] [--smoke] [--force]"

// e2eRecordHelp is what `aru e2e:record --help` prints under the usage line.
const e2eRecordHelp = `It opens Chrome with a window, at the address given -- a URL, a path, or the
name of a route -- on the application already running (aru dev), and records
what you do: the links and buttons you click, the fields you fill, the options
you choose, the boxes you tick, the forms you submit and the pages they lead
to. Press Enter here, or close the window, and it writes
tests/E2E/<Journey>_test.go in the test kit's words: role, label and text
locators, route names for every address a route answers, and an ExpectURL
after each step that moved to another page.

A password typed into a password field is not written down: the journey gets
a placeholder to replace with the fixture's.

The journey opens its page through tests.Serve and tests.Open, which
tests/TestCase.go declares; the command says what to paste when it does not.
The data the recording saw is the running application's, so an id in an
address is that database's: arrange the rows the journey needs in the test.`

// recordSignal is the console message the recorder script reports through.
const recordSignal = "__arandu_record__"

// recordedLocator is how the recorder named an element, in the kit's words.
type recordedLocator struct {
	Kind string `json:"kind"` // role, label, text, testid, css
	A    string `json:"a"`
	B    string `json:"b,omitempty"`
}

// recordedEvent is one message of the recorder script.
type recordedEvent struct {
	Kind   string          `json:"kind"` // load, click, fill, select, check, uncheck, submit, press
	Loc    recordedLocator `json:"loc"`
	Value  string          `json:"value,omitempty"`
	Secret bool            `json:"secret,omitempty"`
	URL    string          `json:"url,omitempty"`
}

// recording collects what the page reported, in order.
type recording struct {
	mu         sync.Mutex
	events     []recordedEvent
	lastAction time.Time
	changed    chan struct{}
}

func newRecording() *recording { return &recording{changed: make(chan struct{}, 1)} }

// event is handed every event the driver reports; the recorder's messages
// arrive as console messages carrying recordSignal.
func (r *recording) event(e browser.Event) {
	if e.Kind != browser.EventConsole {
		return
	}
	payload, ok := strings.CutPrefix(e.Text, recordSignal+" ")
	if !ok {
		return
	}
	var ev recordedEvent
	if json.Unmarshal([]byte(payload), &ev) != nil || ev.Kind == "" {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// snapshot returns the events so far.
func (r *recording) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedEvent(nil), r.events...)
}

// serialDriver lets the recorder ask the browser whether it is still open
// while something else drives it: a Driver is called from one goroutine at a
// time.
type serialDriver struct {
	mu sync.Mutex
	browser.Driver
}

func (s *serialDriver) Evaluate(ctx context.Context, expr string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Driver.Evaluate(ctx, expr, out)
}

func (s *serialDriver) Navigate(ctx context.Context, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Driver.Navigate(ctx, url)
}

func (s *serialDriver) Click(ctx context.Context, x, y float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Driver.Click(ctx, x, y)
}

func (s *serialDriver) InsertText(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Driver.InsertText(ctx, text)
}

func (s *serialDriver) Press(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Driver.Press(ctx, key)
}

// record installs the recorder in d, opens start and collects until stop is
// closed, ctx ends or the browser goes away.
func record(ctx context.Context, d *serialDriver, start string, rec *recording, stop <-chan struct{}) error {
	d.Listen(rec.event)
	if err := d.AddInitScript(ctx, recorderScript); err != nil {
		return fmt.Errorf("installing the recorder: %w", err)
	}
	if err := d.Navigate(ctx, start); err != nil {
		return fmt.Errorf("opening %s: %w", start, err)
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ctx.Done():
			return nil
		case <-tick.C:
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			var one int
			err := d.Evaluate(probe, "1", &one)
			cancel()
			if err != nil && !errors.Is(err, browser.ErrDocumentChanged) && probe.Err() == nil {
				// The window was closed: the pipe is gone.
				return nil
			}
		}
	}
}

// recordedSteps turns the recorder's events into the statements of a
// journey. A page that loads within a few seconds of an action is where the
// action led, and becomes an ExpectURL; one that loads with no action before it
// is a visit.
func recordedSteps(events []recordedEvent, routes []namedRoute, base *url.URL) []string {
	var steps []string
	acted := false
	lastURL := ""
	for _, ev := range events {
		switch ev.Kind {
		case "load":
			if ev.URL == "" || strings.HasPrefix(ev.URL, "about:") || ev.URL == lastURL {
				continue
			}
			target := strings.Join(routeTarget(routes, base, ev.URL), ", ")
			if acted {
				steps = append(steps, "page.ExpectURL("+target+")")
			} else {
				steps = append(steps, "page.Visit("+target+")")
			}
			lastURL, acted = ev.URL, false
			continue
		case "click":
			steps = append(steps, clickStep(ev.Loc))
		case "fill":
			value := goString(ev.Value)
			if ev.Secret {
				steps = append(steps, "// The password typed while recording is not written down: put the fixture's here.")
				value = goString("password")
			}
			if ev.Loc.Kind == "label" {
				steps = append(steps, "page.Fill("+goString(ev.Loc.A)+", "+value+")")
			} else {
				steps = append(steps, "page.FillIn("+locatorExpr(ev.Loc)+", "+value+")")
			}
		case "select":
			if ev.Loc.Kind == "label" {
				steps = append(steps, "page.Select("+goString(ev.Loc.A)+", "+goString(ev.Value)+")")
			} else {
				steps = append(steps, "page.SelectIn("+locatorExpr(ev.Loc)+", "+goString(ev.Value)+")")
			}
		case "check", "uncheck":
			switch {
			case ev.Loc.Kind == "label" && ev.Kind == "check":
				steps = append(steps, "page.Check("+goString(ev.Loc.A)+")")
			case ev.Loc.Kind == "label":
				steps = append(steps, "page.Uncheck("+goString(ev.Loc.A)+")")
			default:
				steps = append(steps, fmt.Sprintf("page.CheckIn(%s, %t)", locatorExpr(ev.Loc), ev.Kind == "check"))
			}
		case "submit":
			steps = append(steps, "page.Submit()")
		case "press":
			steps = append(steps, "page.Press("+goString(ev.Value)+")")
		default:
			continue
		}
		acted = true
	}
	return steps
}

// clickStep is the click on an element, in the shortest of the kit's words.
func clickStep(l recordedLocator) string {
	switch l.Kind {
	case "role":
		return "page.ClickRole(" + goString(l.A) + ", " + goString(l.B) + ")"
	case "text":
		return "page.ClickText(" + goString(l.A) + ")"
	}
	return "page.Click(" + locatorExpr(l) + ")"
}

// locatorExpr is a locator as a browser package expression. A CSS locator
// carries the reason the kit asks for: the recorder found nothing else.
func locatorExpr(l recordedLocator) string {
	switch l.Kind {
	case "role":
		return "browser.Role(" + goString(l.A) + ", " + goString(l.B) + ")"
	case "label":
		return "browser.Label(" + goString(l.A) + ")"
	case "text":
		return "browser.Text(" + goString(l.A) + ")"
	case "testid":
		return "browser.TestID(" + goString(l.A) + ")"
	}
	return "browser.CSS(" + goString(l.A) + ", " + goString("the recorder found no role, label, text or test id for it") + ")"
}

// recordLaunch starts the browser the recorder drives. A variable so a test
// can launch it headless.
var recordLaunch = func(ctx context.Context) (browser.Driver, error) {
	return cdp.Launch(ctx, browser.Options{Browser: "chromium", Headed: true})
}

// e2eRecord is the command.
func e2eRecord(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("e2e:record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "the journey: Checkout writes tests/E2E/Checkout_test.go")
	base := fs.String("base", defaultHost(), "where the application is running")
	smoke := fs.Bool("smoke", false, "name the test TestSmoke<Journey>, which aru test --smoke runs")
	force := fs.Bool("force", false, "replace a journey of the same name")
	target, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("e2e:record: %w", err)
	}
	if target == "" && fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: %s", e2eRecordUsage)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}
	if missing := missingHelpers(root, "Serve", "Open"); len(missing) > 0 {
		return fmt.Errorf("e2e:record: the journey opens its page with tests.%s, and tests/ does not declare it.\n\n%s",
			strings.Join(missing, " and tests."), journeyHelpers)
	}
	baseURL, err := url.Parse(strings.TrimRight(*base, "/"))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return fmt.Errorf("e2e:record: --base=%s is not an address such as http://127.0.0.1:8080", *base)
	}
	routes := projectRoutes(root)
	start, startRoute, err := recordStart(target, baseURL, routes)
	if err != nil {
		return err
	}
	j := journey{Name: journeyName(*name), Smoke: *smoke, ModulePath: modulePath}
	if *name == "" {
		j.Name = journeyName(startRoute)
	}
	if !*force {
		if _, err := os.Stat(root + "/" + j.path()); err == nil {
			return fmt.Errorf("e2e:record: %s already exists: choose another --name, or --force to replace it", j.path())
		}
	}
	if err := reachable(start); err != nil {
		return err
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	d, err := recordLaunch(ctx)
	if err != nil {
		return fmt.Errorf("e2e:record: %w", err)
	}
	defer func() { _ = d.Close() }()

	stop := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		close(stop)
	}()
	fmt.Fprintf(stdout, "Recording %s in Chrome. Use the application, then press Enter here or close the window.\n", start)
	rec := newRecording()
	if err := record(ctx, &serialDriver{Driver: d}, start, rec, stop); err != nil {
		return fmt.Errorf("e2e:record: %w", err)
	}
	return writeRecording(root, j, rec.snapshot(), routes, baseURL, *force, stdout)
}

// writeRecording writes the journey the events describe.
func writeRecording(root string, j journey, events []recordedEvent, routes []namedRoute, base *url.URL, force bool, stdout io.Writer) error {
	j.Steps = recordedSteps(events, routes, base)
	if len(j.Steps) == 0 {
		return errors.New("e2e:record: nothing was recorded, so no journey was written")
	}
	j.Doc = []string{
		j.testName() + " was recorded with aru e2e:record. The data it saw was the running",
		"application's: arrange the rows it needs in the test, and end it with what the",
		"person expects to read -- page.ExpectText -- before keeping it.",
	}
	file, err := renderJourney(j)
	if err != nil {
		return err
	}
	if _, _, err := gen.Write(root, []gen.File{file}, force); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %s with %d steps\nRun it with: aru test --e2e -run '^%s$'\n", file.Path, len(j.Steps), j.testName())
	return nil
}

// recordStart answers the address to open and the route name it is, from what
// was typed: a URL, a path, a route name, or nothing for the home page.
func recordStart(target string, base *url.URL, routes []namedRoute) (string, string, error) {
	switch {
	case target == "":
		name, _, _ := matchRoute(routes, "/")
		return base.String() + "/", name, nil
	case strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://"):
		u, err := url.Parse(target)
		if err != nil {
			return "", "", fmt.Errorf("e2e:record: %s is not an address: %w", target, err)
		}
		name, _, _ := matchRoute(routes, u.Path)
		return target, name, nil
	case strings.HasPrefix(target, "/"):
		name, _, _ := matchRoute(routes, target)
		return base.String() + target, name, nil
	}
	for _, r := range routes {
		if r.Name != target {
			continue
		}
		if strings.Contains(r.Pattern, "{") {
			return "", "", fmt.Errorf("e2e:record: the route %s is %s, which takes parameters: give the address instead", r.Name, r.Pattern)
		}
		return base.String() + r.Pattern, r.Name, nil
	}
	return "", "", fmt.Errorf("e2e:record: %s is neither an address, a path nor the name of a GET route of this project", target)
}

// reachable says the application is not running before a browser is opened
// on an error page.
func reachable(address string) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(address)
	if err != nil {
		return fmt.Errorf("e2e:record: nothing answers at %s. Start the application first, with aru dev, or pass --base", address)
	}
	_ = resp.Body.Close()
	return nil
}
