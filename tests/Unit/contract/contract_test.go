package contract_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/contract"
	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/skills"
	"github.com/arandu-io/aru/tests"
)

// TestEveryRuleACardNamesIsADoctorRule: a card that says a rule verifies it
// names a rule `aru doctor --list` prints. A renamed rule would otherwise
// leave the card promising a check nothing runs.
func TestEveryRuleACardNamesIsADoctorRule(t *testing.T) {
	known := map[string]bool{}
	for _, r := range doctor.List() {
		known[r.Name] = true
	}
	for _, rule := range contract.Rules() {
		if !known[rule] {
			t.Errorf("a card names %q, which the doctor does not check", rule)
		}
	}
}

// TestTheRecipesTheContractDecidedAreThere: the six recipes `aru mcp`
// promises by name, each touching at least one card and saying how in steps.
func TestTheRecipesTheContractDecidedAreThere(t *testing.T) {
	for _, name := range []string{"crud", "action", "nested", "job", "webhook", "integration"} {
		r, ok := contract.RecipeNamed(name)
		if !ok {
			t.Errorf("no recipe %q", name)
			continue
		}
		if len(r.Steps) == 0 || len(r.Cards) == 0 {
			t.Errorf("recipe %q has no steps or touches no card: %+v", name, r)
		}
	}
}

// TestEveryCardSaysWhereAndHow: a card without a path or a signature is a
// card that cannot answer where_does_it_go, and an import that is not
// canonical teaches the path the doctor reports.
func TestEveryCardSaysWhereAndHow(t *testing.T) {
	for _, c := range contract.Cards() {
		if c.Path == "" || c.Signature == "" || c.Title == "" {
			t.Errorf("card %q lacks a path, a signature or a title", c.Kind)
		}
		for _, imp := range c.Imports {
			if strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/security") ||
				strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/data") ||
				strings.HasPrefix(imp.Path, "github.com/arandu-io/framework/validation") {
				t.Errorf("card %q imports %s, a bridge whose names live in hesape", c.Kind, imp.Path)
			}
		}
	}
}

// TestAFindingCarriesTheCardItsRuleVerifies: the doctor reads the contract,
// so a finding names the kind of code whose shape answers it.
func TestAFindingCarriesTheCardItsRuleVerifies(t *testing.T) {
	findings, err := doctor.Run(tests.Fixture(t, "doctor", "violations"), doctor.Conventional)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, f := range findings {
		seen[f.Rule] = f.Contract
	}
	for rule, kind := range map[string]string{
		"handler-reaches-data":          "controller",
		"sensitive-field-not-redacted":  "model",
		"client-outside-clients":        "client",
		"csrf-exempt-without-signature": "webhook",
	} {
		got, fired := seen[rule]
		if !fired {
			t.Errorf("%s did not fire on the violations fixture", rule)
			continue
		}
		if got != kind {
			t.Errorf("%s carries the card %q, want %q", rule, got, kind)
		}
	}
}

// TestTheWebhookRecipeStoresAnEventAndDispatchesNoJob: a received webhook is
// verified, stored as an event in the outbox and handled by a listener after
// the commit. The recipe used to have the service dispatch a job, and a job
// whose handler takes a service imports app/Services: the service importing
// app/Jobs back is an import cycle, so the recipe taught code that does not
// compile once any job takes a service.
func TestTheWebhookRecipeStoresAnEventAndDispatchesNoJob(t *testing.T) {
	r, ok := contract.RecipeNamed("webhook")
	if !ok {
		t.Fatal("no webhook recipe")
	}
	steps := strings.Join(r.Steps, "\n")
	for _, want := range []string{"webhook.Verify", "CSRFExcept", "aru make:event", "outbox.Store", "database.Transaction", "aru make:listener", "listeners.Each"} {
		if !strings.Contains(steps, want) {
			t.Errorf("the webhook recipe does not say %q:\n%s", want, steps)
		}
	}
	for _, step := range r.Steps {
		if strings.HasPrefix(step, "aru make:job") || strings.Contains(step, "dispatches a job") {
			t.Errorf("the webhook recipe has the service dispatch a job: %q", step)
		}
	}
	if strings.Join(r.Cards, ",") != "webhook,event,listener,service" {
		t.Errorf("the webhook recipe touches %v, want webhook, event, listener and service", r.Cards)
	}

	card, _ := contract.Lookup("webhook")
	if strings.Join(card.Generators, ",") != "make:controller,make:request,make:event,make:listener" {
		t.Errorf("the webhook card names the generators %v", card.Generators)
	}
	for _, step := range card.May {
		if strings.Contains(step, "job") {
			t.Errorf("the webhook card may %q", step)
		}
	}
	if verified, _ := contract.ForRule("csrf-exempt-without-signature"); verified.Kind != "webhook" {
		t.Errorf("csrf-exempt-without-signature verifies the card %q, want webhook", verified.Kind)
	}
}

// TestTheJobRecipeDispatchesFromWhereTheQueueIs: a job is dispatched by a
// listener after the write commits, or by a scheduled task, and never by the
// service. A job whose handler takes a service imports app/Services, so a
// service that dispatched it would import app/Jobs back -- an import cycle,
// and the skeleton's SendNotesDigest takes NoteService. The recipe used to say
// "dispatch it from the service" and the service card "record an event or
// dispatch a job", which taught code that does not compile once any job takes
// a service.
func TestTheJobRecipeDispatchesFromWhereTheQueueIs(t *testing.T) {
	r, ok := contract.RecipeNamed("job")
	if !ok {
		t.Fatal("no job recipe")
	}
	steps := strings.Join(r.Steps, "\n")
	for _, want := range []string{
		"aru make:job", "--services=", "registerHandlers", "bootstrap/background.go",
		"outbox.Store", "database.Transaction", "aru make:listener", "listeners.Each",
		"auth.SystemGrant(<action>, e.TenantID)", "//arandu:system-grant",
		"Schedule()", "app/Providers/AppServiceProvider.go", "import cycle",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("the job recipe does not say %q:\n%s", want, steps)
		}
	}
	for _, stale := range []string{"from the service with", "routes/console.go", "handler catalogue"} {
		if strings.Contains(steps, stale) {
			t.Errorf("the job recipe still says %q:\n%s", stale, steps)
		}
	}
	if strings.Join(r.Cards, ",") != "job,event,listener,service" {
		t.Errorf("the job recipe touches %v, want job, event, listener and service", r.Cards)
	}

	service, _ := contract.Lookup("service")
	for _, step := range service.May {
		if strings.Contains(step, "dispatch a job") || strings.Contains(step, "or dispatch") {
			t.Errorf("the service card may %q, and a service that dispatches a job taking services is an import cycle", step)
		}
	}
	if may := strings.Join(service.May, "\n"); !strings.Contains(may, "outbox.Store") || !strings.Contains(may, "a listener dispatches the job") {
		t.Errorf("the service card does not say how its write leads to a job:\n%s", may)
	}
	if mayNot := strings.Join(service.MayNot, "\n"); !strings.Contains(mayNot, "import app/Jobs") {
		t.Errorf("the service card does not forbid importing app/Jobs:\n%s", mayNot)
	}

	listener, _ := contract.RecipeNamed("listener")
	if !strings.Contains(strings.Join(listener.Steps, "\n"), "listeners.Each") {
		t.Errorf("the listener recipe does not say where the listener goes:\n%s", strings.Join(listener.Steps, "\n"))
	}
}

// TestNoCardOrRecipeHasAServiceDispatchAJob: the job card's calledBy names
// the worker, a listener and a scheduled task, and no text of any card or
// recipe has a service dispatch a job, in any sentence. The recipe and the
// service card were corrected once, and the job card is read by the same
// tools, so a single "called by a service" left on it taught the import cycle
// back.
func TestNoCardOrRecipeHasAServiceDispatchAJob(t *testing.T) {
	job, ok := contract.Lookup("job")
	if !ok {
		t.Fatal("no job card")
	}
	for _, entry := range job.CalledBy {
		if strings.Contains(strings.ToLower(entry), "service") {
			t.Errorf("the job card is called by %q, and a service that dispatches a job taking services is an import cycle", entry)
		}
	}
	callers := strings.Join(job.CalledBy, "\n")
	for _, want := range []string{"registerHandlers", "a listener", "Schedule()"} {
		if !strings.Contains(callers, want) {
			t.Errorf("the job card's calledBy does not name %q:\n%s", want, callers)
		}
	}

	seen := map[string]bool{}
	check := func(where, kind, text string) {
		for _, who := range dispatchers(text, kind) {
			seen[who] = true
			if !allowedDispatcher[who] {
				t.Errorf("%s has a %s dispatch a job: %q", where, who, text)
			}
		}
	}
	for _, c := range contract.Cards() {
		for _, text := range c.CalledBy {
			check("the "+c.Kind+" card's calledBy", "", text)
		}
		for _, text := range c.May {
			check("the "+c.Kind+" card's may", c.Kind, text)
		}
		check("the "+c.Kind+" card's errors", c.Kind, c.Errors)
	}
	for _, r := range contract.Recipes() {
		for _, step := range r.Steps {
			check("the "+r.Name+" recipe", "", step)
		}
	}
	// The scan has to recognise the dispatches the contract does teach, or a
	// reader that finds no dispatch at all would pass on any text.
	for _, who := range []string{"listener", "schedule"} {
		if !seen[who] {
			t.Errorf("the scan found no dispatch by a %s, and the contract teaches one: it reads nothing", who)
		}
	}
}

// TestTheDispatchReaderNamesWhoDispatches pins the reader the test above
// relies on to the sentences the contract carried and carries.
func TestTheDispatchReaderNamesWhoDispatches(t *testing.T) {
	for _, c := range []struct{ kind, text, want string }{
		{"service", "6. record an event or dispatch a job", "service"},
		{"", "dispatch it from the service with the Grant it already holds", "service"},
		{"", "work that follows a write: the service stores an event, and a listener built with the queue dispatches the job after the commit", "listener"},
		{"", "work on a clock: a task in the Schedule() of app/Providers/AppServiceProvider.go dispatches it, with the queue the provider holds", "schedule"},
		{"listener", "dispatch a job, with the queue it was built with", "listener"},
		{"", "the service never dispatches the job: app/Jobs imports app/Services", ""},
		{"", "the controller answers 2xx; the service dispatches no job", ""},
		{"", "Dispatch<Name>: a listener once the write has committed", ""},
		{"controller", "call a service", ""},
	} {
		got := strings.Join(dispatchers(c.text, c.kind), ",")
		if got != c.want {
			t.Errorf("%q (card %q) is a dispatch by %q, want %q", c.text, c.kind, got, c.want)
		}
	}
}

// allowedDispatcher is who holds the queue: a listener built with it, or a
// task in a provider's Schedule().
var allowedDispatcher = map[string]bool{"listener": true, "task": true, "schedule": true}

var (
	clauseBreak  = regexp.MustCompile(`; |: |, and |, or | -- `)
	dispatchVerb = regexp.MustCompile(`\b(dispatch|dispatches|dispatched|dispatching|enqueue|enqueues)\b`)
	agent        = regexp.MustCompile(`\b(service|listener|task|schedule|controller|command|worker|middleware|model|repository|policy|client|request)s?\b`)
	fromAgent    = regexp.MustCompile(`^\s+(?:it|a job|the job)\s+from\s+(?:the|a)\s+(\w+)`)
	jobObject    = regexp.MustCompile(`^\s+(a |the )?(job|it)\b`)
)

// dispatchers answers who each affirmative "dispatch a job" clause of text
// has dispatching it: the last kind of code named before the verb, the one
// after "from the", or kind -- the card the text belongs to -- when the clause
// names nobody. A negated clause ("never dispatches", "dispatches no job") and
// a Dispatch<Name> identifier are not a dispatch.
func dispatchers(text, kind string) []string {
	var out []string
	for _, clause := range clauseBreak.Split(text, -1) {
		loc := dispatchVerb.FindStringIndex(clause)
		if loc == nil {
			continue
		}
		before, after := strings.ToLower(clause[:loc[0]]), strings.ToLower(clause[loc[1]:])
		if !jobObject.MatchString(after) {
			continue
		}
		if strings.Contains(before, "never") || strings.Contains(before, " not ") {
			continue
		}
		who := kind
		if names := agent.FindAllStringSubmatch(before, -1); len(names) > 0 {
			who = names[len(names)-1][1]
		} else if m := fromAgent.FindStringSubmatch(after); m != nil {
			who = strings.TrimSuffix(m[1], "s")
		}
		if who == "" {
			who = "caller the text does not name"
		}
		out = append(out, who)
	}
	return out
}

// TestTheEventListenerAndJobCardsNameTheirExample: the skeleton carries one of
// each, and a card that names none leaves a model writing the shape from the
// signature alone.
func TestTheEventListenerAndJobCardsNameTheirExample(t *testing.T) {
	for kind, want := range map[string]string{
		"event":    "app/Events/NotePublished.go",
		"listener": "app/Listeners/NotifyNoteAuthor.go",
		"job":      "app/Jobs/SendNotesDigest.go",
	} {
		card, ok := contract.Lookup(kind)
		if !ok {
			t.Errorf("no %s card", kind)
			continue
		}
		if card.Example != want {
			t.Errorf("the %s card names the example %q, want %q", kind, card.Example, want)
		}
	}
}

// TestEveryExampleIsAFileOfThePinnedSkeleton: a card's example is a path in
// the skeleton release `aru new` creates projects from, read from that
// release in the module cache rather than from a checkout beside this one. A
// path ending in a slash is a directory; any other is a file. An example the
// skeleton renamed or removed would send the reader to nothing.
func TestEveryExampleIsAFileOfThePinnedSkeleton(t *testing.T) {
	dir := pinnedSkeleton(t)
	named := 0
	for _, c := range contract.Cards() {
		if c.Example == "" {
			continue
		}
		named++
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.Example)))
		switch {
		case err != nil:
			t.Errorf("the %s card names %s, which %s@%s does not have", c.Kind, c.Example, skills.SkeletonModule, skills.SkeletonVersion)
		case strings.HasSuffix(c.Example, "/") != info.IsDir():
			t.Errorf("the %s card names %s, and in the skeleton it is a directory: %v", c.Kind, c.Example, info.IsDir())
		}
	}
	if named == 0 {
		t.Fatal("no card names an example, so nothing was checked")
	}
}

// pinnedSkeleton answers the directory of the skeleton release in the module
// cache, downloading it when it is not there. Off CI a machine that cannot
// reach it skips, as the compile harness does; on CI it fails.
func pinnedSkeleton(t *testing.T) string {
	t.Helper()
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("CI") != "" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	tool, err := exec.LookPath("go")
	if err != nil {
		unavailable("the Go toolchain is unavailable: %v", err)
	}
	module := skills.SkeletonModule + "@" + skills.SkeletonVersion
	cmd := exec.Command(tool, "mod", "download", "-json", module)
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	var downloaded struct{ Dir, Error string }
	if jsonErr := json.Unmarshal(out, &downloaded); jsonErr != nil || err != nil || downloaded.Error != "" || downloaded.Dir == "" {
		unavailable("%s could not be downloaded: %v %s\n%s", module, err, downloaded.Error, out)
	}
	return downloaded.Dir
}
