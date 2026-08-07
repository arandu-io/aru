package doctor_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// TestPlantedViolationsAreCaught is exit criterion 2 of phase 2: doctor has to
// fail on violations planted on purpose. The fixture under testdata contains one
// of each, written the way each mistake is actually written -- and every rule
// here corresponds to a real way to lose data or bypass a policy.
func TestPlantedViolationsAreCaught(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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
		// This one had never fired: callName gave up on a nested selector, so
		// `r.Header.Get` rendered as "Get" and the rule matched nothing. Found
		// by audit -- and its own comment calls the header "the form that looks
		// harmless".
		"tenant-from-header": "any client can send the header",
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
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/violations")
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
	first, err := doctor.Run("testdata/violations")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	second, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/clean")
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
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "permission-") || f.Rule == "module-without-manifest" {
			t.Errorf("a correctly declared module produced %s: %s", f.Rule, f.Message)
		}
	}
}

// TestAlpineReachingTheServerIsCaught: without this check, RULE 9's "Alpine only
// where HTMX cannot reach" is opinion, and opinion does not survive a code
// review at 6pm.
func TestAlpineReachingTheServerIsCaught(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var caught *doctor.Finding
	for i, f := range findings {
		if f.Rule == "alpine-reaches-the-server" {
			caught = &findings[i]
			break
		}
	}
	if caught == nil {
		t.Fatal("an x-data with a fetch call was not caught")
	}
	if caught.Severity != doctor.Error {
		t.Error("Alpine fetching from the server is a warning: it is a second data path")
	}
	// The view file, whatever the engine spells it. It was ".templ"; kyse
	// spells it ".kyse.go", and hardcoding one of the two makes this test about
	// the engine rather than about the rule.
	if !strings.Contains(caught.File, "resources/views/") {
		t.Errorf("the finding does not point at the view: %s", caught.File)
	}
	if caught.Line <= 1 {
		t.Errorf("the finding has no useful line: %d", caught.Line)
	}
}

// TestAlpineWithinItsLimitIsSilent: a dropdown is exactly what doc 14 permits,
// and firing on it would teach people to ignore the rule.
func TestAlpineWithinItsLimitIsSilent(t *testing.T) {
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "alpine-reaches-the-server" {
			t.Errorf("client-only state was reported: %s", f.Message)
		}
	}
}

// TestATenantIsFoundByWhereItGoesNotByItsName is a gap an audit found.
//
// The rule asked whether a header was CALLED something with "tenant" in it:
//
//	org := r.Header.Get("X-Org")
//	g := security.SystemGrant(ActionView, org)
//
// passed, and it is exactly the hole RULE 14 exists to close. Whoever writes the
// client picks the header name, so the name proves nothing; what makes a value a
// tenant is that it scopes SQL.
func TestATenantIsFoundByWhereItGoesNotByItsName(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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

// TestAFileThatDoesNotParseIsReportedNotSwallowed is a bug an audit found, and
// the worst kind: doctor did not merely miss something, it invented a finding.
//
// An unparsable file was skipped silently. Every rule reasons over the whole
// file set, so a module whose policy.go does not parse looks exactly like a
// module with no policy -- and doctor told the author to write one they had
// already written, pointing at the wrong file.
func TestAFileThatDoesNotParseIsReportedNotSwallowed(t *testing.T) {
	findings, err := doctor.Run("testdata/broken")
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

// TestTheTreeIsLaravels is what ADR 0019 asks for by name: a test that fails if
// the path detection changes again.
//
// The doctor was written against modules/<name>/, and file.module was filled
// only for paths starting with "modules/". With the Laravel tree it was empty
// for every file, so six rules stopped reporting -- and a rule that concludes
// from an absence does not fail when it goes blind, it passes. A doctor that is
// green because it did not look is worse than no doctor.
func TestTheTreeIsLaravels(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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
			t.Errorf("%s did not fire anywhere in the Laravel tree", c.rule)
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

// TestAControllerReachingTheRepositoryIsCaught is the boundary ADR 0019 calls
// the other 20%.
//
// In Laravel, Service and Repository are the convention of an organized team. A
// controller that holds the repository would have to issue the Grant itself,
// which is the controller authorizing itself -- and the compiler cannot see it,
// because the signature is satisfied.
func TestAControllerReachingTheRepositoryIsCaught(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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

// TestAMapAsViewDataIsCaught is what doc 14 asks for: the data of a view is a
// typed struct, and a map defeats the only thing the view layer buys.
//
// With a struct, a renamed field does not compile. With map[string]any, a typo
// in a key renders as an empty string -- the page comes up, the total is blank,
// and it is found by a customer.
func TestAMapAsViewDataIsCaught(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "view-data-is-a-map" || f.Rule == "view-does-not-exist" {
			t.Errorf("correct code produced %s: %s", f.Rule, f.Message)
		}
	}
}

// TestAViewNameIsResolvedLikeLaravel: "invoices.index" is
// resources/views/invoices/index.kyse.go, and a controller that renders a
// fragment from a nested directory has to resolve the same way. Getting this
// wrong would make view-does-not-exist fire on every correct project.
func TestAViewNameIsResolvedLikeLaravel(t *testing.T) {
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "view-does-not-exist" {
			t.Fatalf("a view that exists was reported missing: %s", f.Message)
		}
	}
}

// gaps runs doctor over testdata/gaps, the fixture of the shapes that used to
// pass.
//
// Every file in it was written by planting code in a generated project and
// watching doctor come back green. They are kept apart from testdata/violations
// so that the difference between "doctor never checked this" and "doctor checks
// this" stays legible in one directory.
func gaps(t *testing.T) []doctor.Finding {
	t.Helper()
	findings, err := doctor.Run("testdata/gaps")
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

// TestARepositoryMethodThatTakesNoGrantIsCaught is RULE 17 where it is most
// often broken.
//
// The audit began by skipping every method that did not take a Grant, on the
// assumption that the signature was the enforcement. It is not: a method that
// never declares the parameter has nothing to satisfy, so a report, a projection
// or an export reaches the database with no policy anywhere on the path -- and
// passed --strict.
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
	if !strings.Contains(caught.Why, "RULE 17") || !strings.Contains(caught.Why, "doc 26") {
		t.Errorf("the explanation cites neither RULE 17 nor doc 26: %q", caught.Why)
	}
	// The method that does take a Grant and checks it is the control: firing on
	// it would make the rule useless.
	if mentions(findings, "grant-not-received", "List") {
		t.Error("a method that takes a Grant and checks it was reported")
	}
}

// TestADiscardedGrantCheckIsCaught: `_ = g.Check(...)` used to satisfy the
// audit, because it only asked whether a call to Check appeared in the body.
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

// TestASystemGrantIsNotExcusedByItsName: the check used to switch itself off for
// any function whose name contained ensure, seed, job, worker, migrate or
// backfill.
//
// `ensureGrant` passed and `issueGrant`, identical in every other character,
// fired. A rule a rename defeats is a spelling convention, not a check.
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
	clean, err := doctor.Run("testdata/clean")
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

// TestSQLThatLostItsTenantPredicateIsCaught is what doc 15 promises as an error
// and nothing enforced: a module generated with --tenant whose `AND tenant_id =
// ?` somebody deleted.
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
		t.Error("SQL that reaches every tenant is a warning: doc 15 says error")
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

// TestATypeThatMerelyStartsWithRepoIsNotARepository: the classifier asked
// whether the receiver contained "Repo", so ReportPolicy, Reporter and
// Reposition were all repositories.
//
// The clean fixture has a Reporter that takes a Grant and hands it to the
// repository, which is correct code -- and it was reported for not calling
// Check.
func TestATypeThatMerelyStartsWithRepoIsNotARepository(t *testing.T) {
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "grant-") {
			t.Errorf("a read model called Reporter was classified as a repository: %s: %s", f.Rule, f.Message)
		}
	}
}

// TestAlpineIsMatchedInEveryDirectiveAndBothQuotes: the pattern matched x-data,
// x-init and x-effect, with double quotes only.
//
// x-on: and @ are precisely where a network call is written, and a formatter
// that prefers single quotes turned the whole rule off.
func TestAlpineIsMatchedInEveryDirectiveAndBothQuotes(t *testing.T) {
	findings := gaps(t)

	var xon, shorthand, singleQuoted bool
	for _, f := range findings {
		if f.Rule != "alpine-reaches-the-server" {
			continue
		}
		switch {
		case strings.HasPrefix(f.Message, "x-on:"):
			xon = true
		case strings.HasPrefix(f.Message, "@"):
			shorthand = true
		case strings.HasPrefix(f.Message, "x-data"):
			singleQuoted = true
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
		"grant-not-received":            true,
		"grant-check-discarded":         true,
		"sql-without-tenant-scope":      true,
		"system-grant-outside-scope":    true,
		"handler-reaches-data":          true,
		"controller-reaches-repository": true,
		"alpine-reaches-the-server":     true,
		"permission-not-used":           true,
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
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Severity == doctor.Error {
			t.Errorf("a clean project reported %s at %s:%d -- %s", f.Rule, f.File, f.Line, f.Message)
		}
	}
}

// TestConcatenatedSQLIsCaught: the rule named sql-built-with-sprintf only ever
// read fmt.Sprintf, while ADR 0024 stated as fact that it had been widened to
// concatenation -- in the paragraph that argues the project does not need sqlc.
//
// So the one barrier against hand-built SQL had a hole exactly where a decision
// document leaned on it, and the hole was the easier form to write:
//
//	"SELECT id FROM invoices WHERE reference LIKE '%" + term + "%'"
func TestConcatenatedSQLIsCaught(t *testing.T) {
	findings, err := doctor.Run("testdata/violations")
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
	findings, err := doctor.Run("testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == "sql-built-by-concatenation" {
			t.Errorf("a clean project was reported at %s:%d -- %s", f.File, f.Line, f.Message)
		}
	}
}
