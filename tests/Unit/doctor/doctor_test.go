package doctor_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/tests"
)

// fixture is one of the projects under internal/doctor/testdata.
//
// They stay there rather than moving here with this file, and it is not
// deference to where they happened to be: internal/doctor/testdata/broken holds
// Go that does not parse because the doctor has to answer honestly about a file
// it could not read, which is a fact about the doctor. The rule set is also
// enumerated by a test that reads the unexported rules slice and therefore
// cannot leave internal/doctor, so the same four projects are read from two
// packages whatever this file does.
func fixture(t *testing.T, name string) string {
	t.Helper()
	return tests.Fixture(t, "doctor", name)
}

// TestPlantedViolationsAreCaught: doctor has to fail on violations planted on
// purpose. The fixture under testdata contains one
// of each, written the way each mistake is actually written -- and every rule
// here corresponds to a real way to lose data or bypass a policy.
func TestPlantedViolationsAreCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]string{
		"grant-not-checked":            "a Grant for another action would pass",
		"sql-built-with-sprintf":       "interpolated SQL is injection",
		"system-grant-without-tenant":  "a system grant with no tenant reads across every customer",
		"handler-reaches-data":         "a handler at the database skipped the policy",
		"tenant-from-request":          "the client would choose whose data to read",
		"session-not-rotated":          "session fixation",
		"repository-without-policy":    "an entity nobody decided who may reach",
		"sensitive-field-not-redacted": "a password one Dump away from the debug page",
		// This one needs the whole call chain: a callName that gives up on a
		// nested selector renders `r.Header.Get` as "Get" and the rule matches
		// nothing.
		"tenant-from-header":        "any client can send the header",
		"resource-not-reauthorized": "the row was read and not re-authorized",
		// Nothing fails on this one today, which is why nothing else would ever
		// report it: the proxy still serves the deleted module, so the build is
		// green over a dependency with no repository behind it.
		"retired-module": "the project is pinned to a repository that was deleted",

		// The four layout checks, borrowed from internal/testlayout. The first
		// is the one worth a tool: a file named Test.go compiles into its
		// package and go test runs nothing in it, with no error and no warning.
		"test-is-not-run":               "a green build over tests that never ran",
		"test-outside-the-tests-tree":   "a test beside the code that reads nothing unexported",
		"package-clause-is-capitalised": "an import that reads as an exported name that is not one",
		"scaffolding-ships":             "the testing package linked into the application",
	}

	found := map[string]bool{}
	for _, f := range findings {
		found[f.Rule] = true
	}

	for rule, why := range want {
		if !found[rule] {
			t.Errorf("doctor did not catch %s -- %s", rule, why)
		}
	}

	if len(findings) < 3 {
		t.Fatalf("only %d findings; the exit criterion asks for at least 3 planted violations", len(findings))
	}
}

// TestFindingsAreActionable: a finding that only names the rule teaches people
// to suppress it. Each one has to say where it is and what breaks.
func TestFindingsAreActionable(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("no findings to check")
	}

	for _, f := range findings {
		if f.File == "" {
			t.Errorf("%s: no file", f.Rule)
		}
		if f.Message == "" {
			t.Errorf("%s: no message", f.Rule)
		}
		if len(f.Why) < 40 {
			t.Errorf("%s: the explanation is too short to act on: %q", f.Rule, f.Why)
		}
		if strings.Contains(strings.ToLower(f.Message), "violation") {
			t.Errorf("%s: the message says a rule was violated instead of what breaks", f.Rule)
		}
	}
}

// TestSeverityIsMeaningful: what blocks a merge has to be what actually breaks
// something. If everything is an error, people stop reading.
func TestSeverityIsMeaningful(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var errors, warnings int
	for _, f := range findings {
		if f.Severity == doctor.Error {
			errors++
		} else {
			warnings++
		}
	}

	if errors == 0 {
		t.Error("nothing in the fixture is an error, and it contains a SQL injection")
	}
	if warnings == 0 {
		t.Error("everything is an error, which is how a tool trains people to ignore it")
	}
}

// TestOrderIsStable: the output feeds CI, and output that reorders between runs
// produces diffs nobody can read.
func TestOrderIsStable(t *testing.T) {
	first, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	second, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("two runs found %d and %d findings", len(first), len(second))
	}
	for i := range first {
		if first[i].Rule != second[i].Rule || first[i].Line != second[i].Line {
			t.Fatalf("finding %d differs between runs: %s vs %s", i, first[i].Rule, second[i].Rule)
		}
	}
}

// TestCleanCodeProducesNothing guards against the failure mode that kills a
// linter: firing on correct code. The fixture here is the shape the generator
// emits, and it must come back silent.
func TestCleanCodeProducesNothing(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, f := range findings {
		if f.Severity == doctor.Error {
			t.Errorf("correct code produced an error: %s", f)
		}
	}
}

// TestAnUndeclaredPermissionIsCaught: the declaration in arandu.mod.toml is the
// only thing anyone reads before installing a module, and a declaration nobody
// verifies is a promise with the weight of a check and the reliability of a
// comment.
func TestAnUndeclaredPermissionIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var caught *doctor.Finding
	for i, f := range findings {
		if f.Rule == "permission-not-declared" {
			caught = &findings[i]
			break
		}
	}
	if caught == nil {
		t.Fatal("a module that declares network = false and calls out was not caught")
	}
	if caught.Severity != doctor.Error {
		t.Error("using an undeclared permission is a warning: the module does something its installer did not agree to")
	}
	if !strings.Contains(caught.Message, "network") {
		t.Errorf("the message does not name the permission: %q", caught.Message)
	}
}

// TestADeclaredPermissionThatIsUsedIsSilent: the clean fixture owns tables and
// says so. Firing there would train people to ignore the rule.
func TestADeclaredPermissionThatIsUsedIsSilent(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "permission-") || f.Rule == "module-without-manifest" {
			t.Errorf("a correctly declared module produced %s: %s", f.Rule, f.Message)
		}
	}
}

// TestADirectiveThatFetchesIsCaught: without this check, "one way to load data"
// is opinion, and opinion does not survive a code review at 6pm.
//
// It is the expensive half of the rule -- a second fetch path with its own
// loading state and its own CSRF handling -- so the message has to say that much
// and not merely name the attribute.
func TestADirectiveThatFetchesIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var caught *doctor.Finding
	for i, f := range findings {
		if f.Rule == "view-keeps-state-in-the-browser" && strings.Contains(f.Message, "fetch()") {
			caught = &findings[i]
			break
		}
	}
	if caught == nil {
		t.Fatalf("an x-data with a fetch call was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("a directive fetching from the server is a warning: it is a second data path")
	}
	// The view file, whatever the engine spells it: hardcoding an extension
	// makes this test about the engine rather than about the rule.
	if !strings.Contains(caught.File, "resources/views/") {
		t.Errorf("the finding does not point at the view: %s", caught.File)
	}
	if caught.Line <= 1 {
		t.Errorf("the finding has no useful line: %d", caught.Line)
	}
}

// TestAnInertDirectiveIsCaughtToo is the half the rule gained, and the reason it
// gained it.
//
// A dropdown written in `x-data` calls nothing and leaks nothing, and it also
// does not work: no asset this stack serves reads the attribute, and the policy
// is script-src 'self' with no unsafe-eval, so nothing would evaluate it even if
// one did. The screen silently does nothing. Reporting only the ones that fetch
// left that case to be discovered by a person clicking a menu that never opens.
func TestAnInertDirectiveIsCaughtToo(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, f := range findings {
		if f.Rule != "view-keeps-state-in-the-browser" {
			continue
		}
		// The inert one is the finding that names no network call.
		if !strings.Contains(f.Message, "fetch()") && !strings.Contains(f.Message, "WebSocket") {
			return
		}
	}
	t.Errorf("a client-only x-data produced no finding, so the rule still only reports the ones that fetch:\n%v", findings)
}

// TestTheShapeTheBehavioursFileReadsIsSilent is where the widening stops.
//
// The clean fixture spells its tabs the way ui.js reads them -- data- attributes
// and the selected one in ARIA -- and a rule that fired there would be a rule
// teaching people to mute it. It is also the guard against the cheap way to
// widen this rule wrong: `hx-get` is not `x-get`, and `data-x-id` is not `x-id`.
func TestTheShapeTheBehavioursFileReadsIsSilent(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "view-keeps-state-in-the-browser" {
			t.Errorf("the data- shape was reported: %s: %s", f.File, f.Message)
		}
	}
}

// TestATenantIsFoundByWhereItGoesNotByItsName.
//
// A rule that asks whether a header is CALLED something with "tenant" in it lets
// this through:
//
//	org := r.Header.Get("X-Org")
//	g := security.SystemGrant(ActionView, org)
//
// Whoever writes the client picks the header name, so the name proves nothing;
// what makes a value a tenant is that it scopes SQL.
func TestATenantIsFoundByWhereItGoesNotByItsName(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, f := range findings {
		if f.Rule != "tenant-from-request" {
			continue
		}
		// The message has to name the variable and the line it came from, or
		// the reader has to go find it.
		if strings.Contains(f.Message, "org") && strings.Contains(f.Message, "read from the request") {
			return
		}
	}
	t.Fatalf("a header named X-Org reaching the tenant of a Grant was not caught:\n%v", findings)
}

// TestAFileThatDoesNotParseIsReportedNotSwallowed covers the worst failure this
// tool has: doctor does not merely miss something, it invents a finding.
//
// Skipping an unparsable file silently is enough to cause it. Every rule reasons
// over the whole file set, so a module whose policy.go does not parse looks
// exactly like a module with no policy -- and doctor tells the author to write
// one they had already written, pointing at the wrong file.
func TestAFileThatDoesNotParseIsReportedNotSwallowed(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "broken"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var reported, invented bool
	for _, f := range findings {
		switch f.Rule {
		case "file-does-not-parse":
			reported = true
			if !strings.Contains(strings.ToLower(f.File), "policy") {
				t.Errorf("the finding names %q, not the file that does not parse", f.File)
			}
		case "repository-without-policy":
			invented = true
		}
	}

	if !reported {
		t.Error("the unparsable file was swallowed")
	}
	if invented {
		t.Error("doctor invented `repository-without-policy` for a module whose policy it could not read")
	}
}

// findRule returns the first finding of a rule, for a test that has something to
// say about one of them.
func findRule(findings []doctor.Finding, rule string) *doctor.Finding {
	for i, f := range findings {
		if f.Rule == rule {
			return &findings[i]
		}
	}
	return nil
}

// TestPathDetectionStillFindsTheAppTree fails if the path detection changes.
//
// A rule that concludes from an absence does not fail when it goes blind, it
// passes: path detection that matches no file makes six rules stop reporting and
// the run come back green. A doctor that is green because it did not look is
// worse than no doctor.
func TestPathDetectionStillFindsTheAppTree(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The rules that reason by entity: each one has to fire on a file under
	// app/, and name the entity rather than a directory.
	for _, c := range []struct {
		rule   string
		entity string
		dir    string
	}{
		{"repository-without-policy", "Billing", "app/Repositories/"},
		{"policy-never-opened", "Charge", "app/Policies/"},
		{"grant-not-checked", "", "app/Repositories/"},
		{"handler-reaches-data", "", "app/Http/Controllers/"},
		{"sensitive-field-not-redacted", "", "app/Models/"},
	} {
		caught := findRule(findings, c.rule)
		if caught == nil {
			t.Errorf("%s did not fire anywhere in the app tree", c.rule)
			continue
		}
		if !strings.HasPrefix(caught.File, c.dir) {
			t.Errorf("%s fired on %s, not under %s", c.rule, caught.File, c.dir)
		}
		if c.entity != "" && !strings.Contains(caught.Message, c.entity) {
			t.Errorf("%s does not name the entity %s: %q", c.rule, c.entity, caught.Message)
		}
	}
}

// TestAControllerReachingTheRepositoryIsCaught covers the boundary between the
// request path and the data.
//
// A controller that holds the repository has to issue the Grant itself, which is
// the controller authorizing itself -- and the compiler cannot see it, because
// the signature is satisfied.
func TestAControllerReachingTheRepositoryIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	caught := findRule(findings, "controller-reaches-repository")
	if caught == nil {
		t.Fatal("a controller importing app/Repositories was not caught")
	}
	if caught.Severity != doctor.Error {
		t.Error("a controller that issues its own Grant is a warning: nothing below it can refuse")
	}
}

// TestAMapAsViewDataIsCaught: the data of a view is a typed struct, and a map
// defeats the only thing the view layer buys.
//
// With a struct, a renamed field does not compile. With map[string]any, a typo
// in a key renders as an empty string -- the page comes up, the total is blank,
// and it is found by a customer.
func TestAMapAsViewDataIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var literal, variable bool
	for _, f := range findings {
		if f.Rule != "view-data-is-a-map" {
			continue
		}
		if f.Severity != doctor.Error {
			t.Error("a map as view data is a warning: the page renders blank instead of failing")
		}
		if strings.Contains(f.Message, "this call passes a map") {
			literal = true
		}
		// The map is usually built on the line above the render, so following
		// the value one step is most of the coverage.
		if strings.Contains(f.Message, "payload") && strings.Contains(f.Message, "line") {
			variable = true
		}
	}

	if !literal {
		t.Error("a map literal passed straight to ctx.View was not caught")
	}
	if !variable {
		t.Errorf("a map built on the line above the render was not caught:\n%v", findings)
	}
}

// TestAViewThatDoesNotExistIsCaught: the name of a view is a string, so this is
// the one thing in the view path the compiler cannot check. It builds, it
// deploys, and the page answers 500 the first time somebody opens it.
func TestAViewThatDoesNotExistIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	caught := findRule(findings, "view-does-not-exist")
	if caught == nil {
		t.Fatal("a render of a view with no source was not caught")
	}
	if !strings.Contains(caught.Message, "billing.missing") {
		t.Errorf("the message does not name the view: %q", caught.Message)
	}
	// The message has to say where to put the file, or the reader has to work
	// out how a view name maps to a path.
	if !strings.Contains(caught.Why, "resources/views/billing/missing.kyse.go") {
		t.Errorf("the explanation does not say where the view goes: %q", caught.Why)
	}
}

// TestTheViewRulesAreSilentOnCorrectCode guards the failure mode that kills a
// linter. The clean fixture renders two views that exist, with the struct each
// one declared in its @go block.
func TestTheViewRulesAreSilentOnCorrectCode(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "view-data-is-a-map" || f.Rule == "view-does-not-exist" {
			t.Errorf("correct code produced %s: %s", f.Rule, f.Message)
		}
	}
}

// TestAValueInTheRawFormIsCaught: `{{ }}` escapes and `{!! !!}` does not, and
// three characters separate them in the source.
//
// The raw form is entitled to a component -- a function returning
// template.HTML, whose own interpolations were escaped when it was generated.
// It is not entitled to a value: a string written there arrives as markup, and
// the day it holds something a person typed, the page runs it for every reader.
//
// The assertions are the three things that make the finding usable: it landed on
// the raw line and not on the escaped one above it, it quotes the expression so
// the reader knows which of several on the page it means, and it names the
// escaped form as the fix.
func TestAValueInTheRawFormIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	caught := findRule(findings, "raw-output-is-not-a-component")
	if caught == nil {
		t.Fatal("a raw interpolation of a plain field was not caught: it reaches the page as markup, unescaped")
	}
	if !strings.Contains(caught.Message, ".Note") {
		t.Errorf("the message does not say which interpolation it means: %q", caught.Message)
	}
	if !strings.Contains(caught.Why, "{{ .Note }}") {
		t.Errorf("the explanation does not name the escaped form as the fix: %q", caught.Why)
	}
	// A warning, because the shape is evidence and not proof: doctor reads the
	// markup and never the types, so a field already holding template.HTML has
	// the same shape and is correct code.
	if caught.Severity != doctor.Warning {
		t.Error("the raw form is an error, and doctor cannot see the type of the expression: a field that already holds template.HTML has this shape and is correct")
	}

	// The escaped interpolation on the line above is not this rule's business,
	// and a rule that fired on both would be a rule nobody could act on.
	for _, f := range findings {
		if f.Rule == "raw-output-is-not-a-component" && strings.Contains(f.Message, ".Title") {
			t.Errorf("the escaped form was reported: %s", f.Message)
		}
	}
}

// TestAComponentInTheRawFormIsAllowed is the other half, and without it the rule
// above is satisfied by a rule that fires on everything.
//
// The clean fixture calls a component from inside a @foreach in a @section,
// which is also the walk: a rule that only looked at the top level would find
// nothing there and pass this test by never running.
func TestAComponentInTheRawFormIsAllowed(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "raw-output-is-not-a-component" {
			t.Errorf("a component call in the raw form was reported: %s", f.Message)
		}
	}
}

// TestADottedViewNameResolvesToItsFile: "invoices.index" is
// resources/views/invoices/index.kyse.go, and a controller that renders a
// fragment from a nested directory resolves the same way. Getting this wrong
// would make view-does-not-exist fire on every correct project.
func TestADottedViewNameResolvesToItsFile(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "view-does-not-exist" {
			t.Fatalf("a view that exists was reported missing: %s", f.Message)
		}
	}
}

// gaps runs doctor over testdata/gaps, the fixture of the shapes that pass every
// other rule.
//
// They are kept apart from testdata/violations so that the difference between
// "doctor never checked this" and "doctor checks this" stays legible in one
// directory.
func gaps(t *testing.T) []doctor.Finding {
	t.Helper()
	findings, err := doctor.Run(fixture(t, "gaps"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return findings
}

// findAt returns the finding of a rule at a file, for a test that cares where it
// landed.
func findAt(findings []doctor.Finding, rule, file string) *doctor.Finding {
	for i, f := range findings {
		if f.Rule == rule && strings.Contains(f.File, file) {
			return &findings[i]
		}
	}
	return nil
}

// mentions reports whether any finding of a rule names something.
func mentions(findings []doctor.Finding, rule, needle string) bool {
	for _, f := range findings {
		if f.Rule == rule && strings.Contains(f.Message, needle) {
			return true
		}
	}
	return false
}

// TestARepositoryMethodThatTakesNoGrantIsCaught covers authorization on the read
// path, where it is most often broken.
//
// Skipping every method that does not take a Grant assumes the signature is the
// enforcement. It is not: a method that never declares the parameter has nothing
// to satisfy, so a report, a projection or an export reaches the database with
// no policy anywhere on the path -- and passes --strict.
func TestARepositoryMethodThatTakesNoGrantIsCaught(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "grant-not-received")
	if caught == nil {
		t.Fatalf("a repository method that queries the database without a Grant was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("a read path with no policy is a warning: it is a tenant leak with a technical name")
	}
	if !mentions(findings, "grant-not-received", "Totals") {
		t.Errorf("the finding does not name the method: %q", caught.Message)
	}
	if !strings.Contains(caught.Why, "security.Grant") || !strings.Contains(caught.Why, "reading is authorized exactly like writing") {
		t.Errorf("the explanation does not say that a read needs a Grant, or what to write: %q", caught.Why)
	}
	// The method that does take a Grant and checks it is the control: firing on
	// it would make the rule useless.
	if mentions(findings, "grant-not-received", "List") {
		t.Error("a method that takes a Grant and checks it was reported")
	}
}

// TestADiscardedGrantCheckIsCaught: `_ = g.Check(...)` satisfies any check that
// only asks whether a call to Check appears in the body.
//
// A method that asks the question and throws the answer away is a method with no
// authorization, spelled to look like one that has it -- which is worse than
// leaving the check out, because a reader stops looking.
func TestADiscardedGrantCheckIsCaught(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "grant-check-discarded")
	if caught == nil {
		t.Fatalf("`_ = g.Check(...)` was accepted as a check:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("discarding the answer of Check is a warning: the method is unauthorized")
	}
	if !strings.Contains(caught.Message, "Purge") {
		t.Errorf("the finding does not name the method: %q", caught.Message)
	}
}

// TestASystemGrantIsNotExcusedByItsName: a check that switches itself off for
// any function whose name contains ensure, seed, job, worker, migrate or
// backfill lets `ensureGrant` past and fires on `issueGrant`, identical in every
// other character. A rule a rename defeats is a spelling convention, not a
// check.
func TestASystemGrantIsNotExcusedByItsName(t *testing.T) {
	findings := gaps(t)

	for _, fn := range []string{"issueGrant", "ensureGrant"} {
		if !mentions(findings, "system-grant-outside-scope", fn) {
			t.Errorf("SystemGrant in %s was not reported:\n%v", fn, findings)
		}
	}
	// And the escape that is meant to work: explicit, on the line, in the diff.
	if mentions(findings, "system-grant-outside-scope", "backfillTotals") {
		t.Error("a call marked //arandu:system-grant with a reason was still reported")
	}

	// The other half of removing an allowlist is not removing the one that was
	// right. database/seeders is where the skeleton itself calls SystemGrant,
	// and a rule that shouts at code the generator wrote is worse than no rule.
	clean, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if caught := findRule(clean, "system-grant-outside-scope"); caught != nil {
		t.Errorf("the seeder the generator writes was reported: %s", caught)
	}
}

// TestTheRequestBoundaryCoversRoutesAndMiddleware: the two rules that keep a
// handler away from the database only looked at app/Http/Controllers.
//
// A handler written inline in the custom block of routes/web.go -- which the
// skeleton invites -- and a middleware holding a repository are the same request
// path with the same database under it, and both walked straight through.
func TestTheRequestBoundaryCoversRoutesAndMiddleware(t *testing.T) {
	findings := gaps(t)

	if findAt(findings, "handler-reaches-data", "routes/web.go") == nil {
		t.Errorf("a handler inline in routes/web.go reached the data package:\n%v", findings)
	}
	if findAt(findings, "controller-reaches-repository", "app/Http/Middleware/") == nil {
		t.Errorf("a middleware holding a repository was not caught:\n%v", findings)
	}
}

// TestSQLThatLostItsTenantPredicateIsCaught: a module generated with --tenant
// whose `AND tenant_id = ?` somebody deleted.
//
// It is a leak between customers in its most direct form, and every other rule
// stays green -- the Grant is taken, the Grant is checked, and the query returns
// every tenant's rows.
func TestSQLThatLostItsTenantPredicateIsCaught(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "sql-without-tenant-scope")
	if caught == nil {
		t.Fatalf("an UPDATE with no tenant predicate was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("SQL that reaches every tenant is reported as a warning, and it is an error")
	}
	// The UPDATE is the one that matters most: it writes.
	if !mentions(findings, "sql-without-tenant-scope", "Archive") {
		t.Errorf("the UPDATE in Archive was not reported:\n%v", findings)
	}
	// The method that scopes its query is the control.
	if mentions(findings, "sql-without-tenant-scope", "List") {
		t.Error("a query that filters by tenant_id was reported")
	}
}

// TestSQLIsScopedByTheTableNotByTheReceiver covers the hole the rule above left
// open everywhere outside app/Repositories.
//
// The check began by asking whether the receiver was a repository, so the same
// statement in a type ending in Service was read by nothing: not by this rule,
// which had stopped, and not by the rules that guard the request path, which end
// at controllers, middleware and routes/. A SELECT with no tenant predicate sat
// in app/Services and every one of the checks came back silent.
//
// The directory is the part that matters. handler-reaches-data tells people, in
// its own sentence, to move the call into app/Services -- so the blind spot was
// the destination the report was sending them to.
//
// This test fails the moment the gate goes back to the receiver, in either of the
// two places it lived: the rule's own loop, and the map of what is multi-tenant,
// which was keyed by receiver type and therefore had no answer for a service.
func TestSQLIsScopedByTheTableNotByTheReceiver(t *testing.T) {
	findings := gaps(t)

	caught := findAt(findings, "sql-without-tenant-scope", "app/Services/ExportService.go")
	if caught == nil {
		t.Fatalf("a SELECT with no tenant predicate outside app/Repositories was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("SQL that reaches every tenant is reported as a warning, and it is an error wherever it was written")
	}
	if !strings.Contains(caught.Message, "Export") {
		t.Errorf("the finding does not name the method: %q", caught.Message)
	}
	// The table is what answered, so the finding says which one: it is where the
	// reader goes to look, and naming the receiver instead would point at the
	// type that happens to hold the handle.
	if !strings.Contains(caught.Message, "reports") {
		t.Errorf("the finding does not name the table: %q", caught.Message)
	}
	// The sibling method reads the same table from the same type with the tenant
	// taken off the Grant. Reporting it would mean the rule fires on its own fix.
	if mentions(findings, "sql-without-tenant-scope", "Monthly") {
		t.Error("a query outside app/Repositories that filters by tenant_id was reported")
	}
}

// TestSQLIsReadInAFunctionThatWasNeverDeclared closes the last cheap way out of
// the rule: hold the body in a package-level var instead of declaring it.
//
// Reading only func declarations meant the same SELECT over the same table was
// an error in ExportService and silent four lines away in HandlerVars. Nothing
// about the shape is exotic -- a handler in a var is how a table of them gets
// built -- and the body runs like any other.
//
// The finding names the var, because that is what the reader opens. Reporting a
// line and no name is what a rule that reads bodies anywhere may not do.
func TestSQLIsReadInAFunctionThatWasNeverDeclared(t *testing.T) {
	findings := gaps(t)

	caught := findAt(findings, "sql-without-tenant-scope", "app/Services/HandlerVars.go")
	if caught == nil {
		t.Fatalf("a SELECT with no tenant predicate inside a package-level var was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("SQL that reaches every tenant is reported as a warning, and it is an error however the function was written")
	}

	// Three bodies, three shapes, each named by the var that holds it: a bare
	// literal, one inside a struct inside a slice, and one nested in another.
	for _, name := range []string{"ExportAll", "exportHandlers", "nestedExport"} {
		if !mentions(findings, "sql-without-tenant-scope", name) {
			t.Errorf("the statement in %s was not reported, or the finding does not name it:\n%v", name, findings)
		}
	}

	// A literal nested in another is reached once. Reading the outer body and
	// then the inner one again would report the same statement twice, and a
	// report that repeats itself is one people learn to skim.
	nested := 0
	for _, f := range findings {
		if f.Rule == "sql-without-tenant-scope" && strings.Contains(f.Message, "nestedExport") {
			nested++
		}
	}
	if nested != 1 {
		t.Errorf("the statement inside a nested literal produced %d findings, want 1", nested)
	}

	// The sibling var reads the same table with the tenant taken off the Grant.
	// Reporting it would mean the rule fires on the fix it asks for.
	if mentions(findings, "sql-without-tenant-scope", "ExportScoped") {
		t.Error("a var-held query that filters by tenant_id was reported")
	}

	// And the same control in the fixture that has to stay silent altogether. A
	// widening pays for itself only if it finds nothing in correct code, and the
	// place that proves it is the project with nothing wrong in it.
	clean, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if caught := findRule(clean, "sql-without-tenant-scope"); caught != nil {
		t.Errorf("the widened rule reported correct code: %s", caught)
	}
}

// TestTheTenantRuleHasExactlyTwoEscapes pins what the widened rule is allowed to
// stay quiet about, because a gate that answers everywhere needs its exceptions
// written down rather than discovered.
//
// A migration is structural: it runs once per database, from the pipeline, with
// no request and no Grant behind it, so data.Tenant is not missing there -- it
// has nothing to read. Everything else is the marker rule 7 already uses, on the
// line, with a reason.
//
// Both fixtures live in testdata/clean, so a broken escape is an error on code
// that is correct -- which is the failure that makes people stop reading a tool.
func TestTheTenantRuleHasExactlyTwoEscapes(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, escape := range []struct {
		file string
		what string
	}{
		{"database/migrations/0001_01_01_000001_backfill_invoice_totals.go",
			"a migration backfill, which has no Grant to take a tenant off"},
		{"database/seeders/DatabaseSeeder.go",
			"a statement marked //arandu:system-grant with a reason"},
	} {
		if caught := findAt(findings, "sql-without-tenant-scope", escape.file); caught != nil {
			t.Errorf("%s was reported: %s", escape.what, caught)
		}
	}
}

// TestATypeThatMerelyStartsWithRepoIsNotARepository: the classifier asked
// whether the receiver contained "Repo", so ReportPolicy, Reporter and
// Reposition were all repositories.
//
// The clean fixture has a Reporter that takes a Grant and hands it to the
// repository, which is correct code -- and it was reported for not calling
// Check.
func TestATypeThatMerelyStartsWithRepoIsNotARepository(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "grant-") {
			t.Errorf("a read model called Reporter was classified as a repository: %s: %s", f.Rule, f.Message)
		}
	}
}

// TestEveryDirectiveAndBothQuotesAreMatched: the pattern matched x-data, x-init
// and x-effect, with double quotes only.
//
// x-on: and @ are precisely where a network call is written, and a formatter
// that prefers single quotes turned the whole rule off. The fourth name is the
// widening: x-show holds no expression anybody would call a data path, and it is
// the shape that used to walk straight through.
func TestEveryDirectiveAndBothQuotesAreMatched(t *testing.T) {
	findings := gaps(t)

	var xon, shorthand, singleQuoted, stateOnly bool
	for _, f := range findings {
		if f.Rule != "view-keeps-state-in-the-browser" {
			continue
		}
		switch {
		case strings.HasPrefix(f.Message, "x-on:"):
			xon = true
		case strings.HasPrefix(f.Message, "@"):
			shorthand = true
		case strings.HasPrefix(f.Message, "x-data") && strings.Contains(f.Message, "WebSocket"):
			singleQuoted = true
		case strings.HasPrefix(f.Message, "x-data"):
			stateOnly = true
		}
	}

	if !xon {
		t.Errorf("x-on:click with a fetch call was not caught:\n%v", findings)
	}
	if !shorthand {
		t.Errorf("@click with a fetch call was not caught:\n%v", findings)
	}
	if !singleQuoted {
		t.Errorf("a single-quoted x-data with a WebSocket was not caught:\n%v", findings)
	}
	if !stateOnly {
		t.Errorf("a client-only x-data was not caught, so the rule is still the narrow one:\n%v", findings)
	}
}

// TestSystemGrantOutsideItsScopeHasAFixture and the one below close a hole a
// mutation harness found: both rules could be deleted whole, with CI green.
//
// A rule no fixture exercises is a rule that will be deleted by the next person
// who finds it inconvenient, and nothing will say so.
func TestSystemGrantOutsideItsScopeHasAFixture(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "system-grant-outside-scope")
	if caught == nil {
		t.Fatalf("no fixture exercises system-grant-outside-scope:\n%v", findings)
	}
	if caught.Severity != doctor.Warning {
		t.Error("a system grant outside its scope is an error: it is legitimate often enough that an error would get muted")
	}
}

// TestAnUnusedPermissionHasAFixture: declared and not used is the half of the
// manifest rule nothing exercised.
func TestAnUnusedPermissionHasAFixture(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "permission-not-used")
	if caught == nil {
		t.Fatalf("no fixture exercises permission-not-used:\n%v", findings)
	}
	if !strings.Contains(caught.Message, "exec") {
		t.Errorf("the message does not name the permission: %q", caught.Message)
	}
	if caught.Severity != doctor.Warning {
		t.Error("asking for a permission you do not use is an error: it is untidy, not dangerous")
	}
}

// TestTheGapsFixtureReportsNothingElse keeps the fixture honest.
//
// Every finding it produces has to be one of the holes it was written for. A
// fixture that also trips three unrelated rules stops being evidence about the
// rule under test.
func TestTheGapsFixtureReportsNothingElse(t *testing.T) {
	expected := map[string]bool{
		"grant-not-received":              true,
		"grant-check-discarded":           true,
		"sql-without-tenant-scope":        true,
		"system-grant-outside-scope":      true,
		"handler-reaches-data":            true,
		"controller-reaches-repository":   true,
		"view-keeps-state-in-the-browser": true,
		"permission-not-used":             true,
		"outbox-not-registered":           true,
	}
	for _, f := range gaps(t) {
		if !expected[f.Rule] {
			t.Errorf("the fixture also trips %s, which it was not written for: %s", f.Rule, f)
		}
	}
}

// TestARepositoryIsSeenByEitherSpelling guards a trade that went the wrong way.
//
// isRepository used strings.Contains(receiver, "Repo"), which classified
// ReportPolicy, Reporter and Reposition as repositories -- three findings on
// correct code. Narrowing it to HasSuffix("Repository") fixed those and went
// blind to every type ending in Repo, in a directory the developer named.
//
// That predicate gates grant-not-received, grant-not-checked,
// grant-check-discarded and sql-without-tenant-scope. A type it does not see is
// a type where none of the four apply, so this false negative was four rules off
// at once -- strictly worse than the noise it replaced.
//
// The fixture is app/Reporting/InvoiceRepo.go: exported method, no Grant, a
// SELECT with no tenant.
func TestARepositoryIsSeenByEitherSpelling(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, f := range findings {
		if strings.Contains(f.File, "InvoiceRepo.go") {
			return
		}
	}

	var rules []string
	for _, f := range findings {
		rules = append(rules, f.Rule+" @ "+f.File)
	}
	t.Errorf("nothing was reported for app/Reporting/InvoiceRepo.go, an exported method with no Grant running an unscoped SELECT.\nreported:\n  %s",
		strings.Join(rules, "\n  "))
}

// TestTheNearMissesAreStillQuiet is the other half: the three names that made
// the substring match noisy have to stay out. A rule that fires on correct code
// is how a tool teaches people to ignore it.
func TestTheNearMissesAreStillQuiet(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Severity == doctor.Error {
			t.Errorf("a clean project reported %s at %s:%d -- %s", f.Rule, f.File, f.Line, f.Message)
		}
	}
}

// TestConcatenatedSQLIsCaught: the rule named sql-built-with-sprintf reads
// fmt.Sprintf alone, which leaves the one barrier against hand-built SQL open to
// the easier form to write:
//
//	"SELECT id FROM invoices WHERE reference LIKE '%" + term + "%'"
func TestConcatenatedSQLIsCaught(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "sql-built-by-concatenation" && strings.Contains(f.File, "SearchRepository.go") {
			if f.Severity != doctor.Error {
				t.Errorf("severity = %v, want Error: it is injection", f.Severity)
			}
			return
		}
	}
	t.Error("a statement built by concatenating a parameter was not reported")
}

// TestTheGeneratorsOwnConcatenationIsQuiet is the other half, and the one that
// decides whether the rule is usable.
//
// The generated repository concatenates on purpose: a package-level const for
// the column list, and a `column` local chosen from an allowlist. Neither can
// carry a value from outside, and a rule that fired on them would fire on every
// project on its first run -- which is how a tool teaches people to ignore it.
func TestTheGeneratorsOwnConcatenationIsQuiet(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "sql-built-by-concatenation" {
			t.Errorf("a clean project was reported at %s:%d -- %s", f.File, f.Line, f.Message)
		}
	}
}

// TestAModuleWhoseWritesNeedAnotherModulesTableIsCaught.
//
// auth.Register publishes a domain event inside the transaction that creates the
// account, so it cannot commit without the outbox table. That table travels with
// events.NewModule(), and both shipped bootstraps register it -- but nothing
// connects the two, so an application that drops the line while tidying compiles,
// passes its tests, and answers 500 to the first person who signs up, with
// "no such table: outbox" on the screen where a failure is least recoverable.
func TestAModuleWhoseWritesNeedAnotherModulesTableIsCaught(t *testing.T) {
	findings := gaps(t)

	caught := findRule(findings, "outbox-not-registered")
	if caught == nil {
		t.Fatalf("a project registering auth and no outbox table was not caught:\n%v", findings)
	}
	if caught.Severity != doctor.Error {
		t.Error("a sign-up that fails every time is a warning: nothing about it works, and CI would pass")
	}
	// auth.NewService and not auth.New, which this fixture also calls: the
	// registration builds nothing and can sit in a controller, while the service
	// is what constructs the outbox and sits in the bootstrap -- which is the
	// file the explanation below tells the reader to edit.
	if !strings.Contains(caught.Message, "auth.NewService") {
		t.Errorf("the finding does not name what needs the table: %q", caught.Message)
	}
	// The reader has to be able to fix it without going to look for the name of
	// the module, and to know what breaks if they do not.
	if !strings.Contains(caught.Why, "events.NewModule()") {
		t.Errorf("the explanation does not say what to add: %q", caught.Why)
	}
	if !strings.Contains(caught.Why, "no such table: outbox") {
		t.Errorf("the explanation does not say what the failure looks like: %q", caught.Why)
	}
	if !strings.Contains(caught.File, "bootstrap/") {
		t.Errorf("the finding points at %s, and the line to add is in the bootstrap", caught.File)
	}

	// One missing line, one finding. This fixture reaches the outbox twice --
	// it builds the service and registers the module -- and saying so twice
	// would be two entries that ask for the same single edit.
	var times int
	for _, f := range findings {
		if f.Rule == "outbox-not-registered" {
			times++
		}
	}
	if times != 1 {
		t.Errorf("the same missing line was reported %d times", times)
	}
}

// The other half, and the one that decides whether the rule is usable: the shape
// both shipped bootstraps have -- auth and the events module registered together
// -- has to come back silent. A rule that fires on the correct wiring is how a
// tool teaches people to ignore it.
func TestRegisteringTheOutboxNextToWhatWritesToItIsSilent(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if caught := findRule(findings, "outbox-not-registered"); caught != nil {
		t.Errorf("the wiring the skeleton ships was reported: %s", caught)
	}
}

// TestTheAggregateRulesAreSilentOnTheConventionalProfile is the half that
// decides whether the profile flag is usable.
//
// The clean fixture holds a join and a transaction over two tables. Both are
// correct SQL on a relational database, and reporting them by default would tell
// every project in the world to redesign a write that works.
func TestTheAggregateRulesAreSilentOnTheConventionalProfile(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rule := range []string{"join-across-aggregates", "transaction-across-aggregates", "profile-not-declared"} {
		if caught := findRule(findings, rule); caught != nil {
			t.Errorf("%s fired without the profile being asked for: %s", rule, caught)
		}
	}
}

// TestTheProfileFlagAddsTheAggregateRules is the other half: the same fixture,
// the same code, one flag, and the two statements that cannot survive the move
// to a wide-column store are named.
//
// It is what stops the flag from being accepted and then doing nothing, which is
// worse than not accepting it: a pipeline that passes `--profile=performance`
// and gets a clean report reads it as a promise the module runs there.
func TestTheProfileFlagAddsTheAggregateRules(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Performance)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	join := findRule(findings, "join-across-aggregates")
	if join == nil {
		t.Fatal("the join was not caught: on the performance profile the two entities are in different partitions and no statement reads both")
	}
	if join.Severity != doctor.Error {
		t.Error("a join is a warning on the performance profile: the query cannot be expressed there at all")
	}
	// Both tables, because the person reading it has to know which second entity
	// the statement reached.
	for _, table := range []string{"invoices", "customers"} {
		if !strings.Contains(join.Message, table) {
			t.Errorf("the message does not name %s: %q", table, join.Message)
		}
	}

	tx := findRule(findings, "transaction-across-aggregates")
	if tx == nil {
		t.Fatal("the transaction over two tables was not caught: nothing commits them together on the performance profile")
	}
	if tx.Severity != doctor.Error {
		t.Error("a transaction across aggregates is a warning: one write lands and the other does not")
	}

	// Two transactions are reported and one is not, and the three are in the
	// fixture for different reasons.
	//
	// Void writes two tables in a transaction of its own, which is the statement
	// half of the check. Settle opens one in a service and calls two
	// repositories, which is the shape a real application has -- the SQL is one
	// level down and there is nothing in the transaction to read, so the check
	// reads the repository fields instead. Note opens one by hand over a single
	// table, after reading another one outside it: a check that counted the
	// method rather than the region inside the transaction reports it, and
	// somebody redesigns a write that was fine.
	reported := map[string]bool{}
	for _, f := range findings {
		if f.Rule != "transaction-across-aggregates" {
			continue
		}
		for _, method := range []string{"Void", "Settle", "Note"} {
			if strings.Contains(f.Message, method) {
				reported[method] = true
			}
		}
	}
	for _, method := range []string{"Void", "Settle"} {
		if !reported[method] {
			t.Errorf("the transaction in %s was not reported", method)
		}
	}
	if reported["Note"] {
		t.Error("a single-aggregate transaction was reported: the region inside the transaction touches one table")
	}

	declared := findRule(findings, "profile-not-declared")
	if declared == nil {
		t.Fatal("the fixture declares profiles = [conventional] and was checked against performance with nothing said")
	}
	if declared.Severity != doctor.Warning {
		t.Error("the missing declaration is an error, so the check that tells you whether you may declare it refuses to run until you have declared it")
	}
}

// TestTheGeneratedRepositoryPassesThePerformanceProfile guards the one shape
// that would make the join rule useless.
//
// Every List `aru make:module` emits carries a keyset cursor written as a
// subquery, and a rule that looked for the word SELECT twice, or for any SQL a
// wide-column store cannot spell, would fire on every module ever generated. The
// subquery reads the repository's own table, which is one aggregate.
func TestTheGeneratedRepositoryPassesThePerformanceProfile(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "clean"), doctor.Performance)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule != "join-across-aggregates" {
			continue
		}
		if !strings.Contains(f.Message, "Overdue") {
			t.Errorf("a statement other than the planted join was reported: %s", f)
		}
	}
}

// TestAnUnknownProfileIsRefused: a value that is not one of the two has to fail
// rather than fall back, or a typo checks less than was asked for and says so
// nowhere.
func TestAnUnknownProfileIsRefused(t *testing.T) {
	if _, err := doctor.ParseProfile("performace"); err == nil {
		t.Fatal("a misspelled profile was accepted")
	}
	for _, name := range []string{"conventional", "performance"} {
		if _, err := doctor.ParseProfile(name); err != nil {
			t.Errorf("ParseProfile(%q): %v", name, err)
		}
	}
}
