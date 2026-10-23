package doctor

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/aru/internal/testlayout"
)

// The rules in this file read the application's test suite: tests/ and every
// _test.go file. They enter as warnings, and become errors on 2026-12-15 --
// the day each Why names -- in the release after it: they report tests that
// pass today and prove less than they say.
//
// A rule here never reads the clock. The severity is the one a release
// declares, like every other rule's, and moving it is a change to this file.

// promotedOn is the day the suite rules stop being warnings.
const promotedOn = "2026-12-15"

// promotion is the sentence every suite rule's Why ends with.
const promotion = " A warning until " + promotedOn + ", an error after it."

// testKitRoots are the import paths of the test kit: the canonical one, and
// the two older spellings a project may still import.
var testKitRoots = []string{
	"github.com/arandu-io/framework/test",
	"github.com/arandu-io/hesape/arandutest",
	"github.com/arandu-io/framework/arandutest",
}

// kitPackage reports whether path is the kit package at sub ("/browser",
// "/conformance", "" for the root) under any of its spellings.
func kitPackage(path, sub string) bool {
	for _, root := range testKitRoots {
		if path == root+sub {
			return true
		}
	}
	return false
}

// suiteFile reports whether the file belongs to the test suite: a _test.go
// file anywhere, or a file of the tests/ tree.
func suiteFile(f *file) bool {
	return f.isTest || strings.HasPrefix(f.rel, "tests/")
}

// requestLayerFile reports whether the file belongs to a layer where a
// subject reaches a handler or a session: every test outside tests/Unit, and
// the helpers under tests/ they sign in with.
func requestLayerFile(f *file) bool {
	return strings.HasPrefix(f.rel, "tests/") && !strings.HasPrefix(f.rel, "tests/Unit/")
}

// journeyFile reports whether the file belongs to the end-to-end layer, or to
// the browser tests it absorbed.
func journeyFile(f *file) bool {
	return strings.HasPrefix(f.rel, testlayout.E2EDir+"/") || strings.HasPrefix(f.rel, "tests/Browser/")
}

// methodName answers the name a call is made by, without its receiver:
// t.Skip is Skip, page.Click is Click, Skip is Skip.
func methodName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// stringArg answers the string literal a call passes at position i.
func stringArg(call *ast.CallExpr, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	lit, ok := call.Args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// 43. A test waits for what it needs, and never sleeps.
//
// Reason. time.Sleep in a test is a guess at how long something takes: too
// short and the test fails on a slow runner, too long and every run pays it,
// and either way it proves nothing about the order things happen in. A
// journey's page waits by itself -- visible and still, htmx settled, network
// idle -- and a TTL or an expiry moves the fake clock instead of the real one.
//
// Scope. Every _test.go file, and every file of tests/.
//
// Severity. A warning until 2026-12-15.
//
// Positive: time.Sleep(2 * time.Second) in tests/E2E/Checkout_test.go, in a
// helper of tests/Helpers, in app/Services/cache_internal_test.go. One finding
// per call.
//
// Negative: time.Sleep in app/ code that ships; a timer or time.After in a
// select with a deadline, which waits for an event and gives up.
//
// Known false positive: a test of a sleeping function's own behaviour.
//
// Limit. A call of time.Sleep through the file's import of time; a sleep
// behind a helper of another package is not followed.
//
// Correction: delete the sleep and let the page wait, or move the clock with
// support.SetTestNow, Travel or FreezeTime.
func testsDoNotSleep(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !suiteFile(f) {
			continue
		}
		f.calls(func(call *ast.CallExpr, _ string) {
			imported, ok := f.importedFunction(callName(call))
			if !ok || imported.pkg != "time" || imported.name != "Sleep" {
				return
			}
			out = append(out, Finding{
				Rule: "test-sleep", Severity: Warning,
				File: f.rel, Line: f.line(call),
				Message: "this test sleeps instead of waiting for what it needs",
				Why: "a sleep is a guess at how long something takes: too short and the test fails on a slow runner, too long " +
					"and every run pays it. A journey's page waits by itself (visible, htmx settled, network idle); a TTL or an " +
					"expiry moves the clock with support.SetTestNow, Travel or FreezeTime." + promotion,
			})
		})
	}
	return out
}

// subjectPackages are where a Subject is declared.
func isSubjectPackage(path string) bool {
	return path == "github.com/arandu-io/framework/security" || path == "github.com/arandu-io/hesape/auth"
}

// 44. A test outside tests/Unit signs in for real.
//
// Reason. A Subject written into a test and handed to a handler or a session
// skips what production does on every request: the session is loaded by the
// guard and the account's roles are answered by the SubjectResolver. A test
// that wrote Roles: admin into a literal passes while the admin screen is
// broken for every real admin. test.As signs the account in on the
// application's own session store, and the resolver answers who it is.
//
// Scope. tests/ outside tests/Unit: the Feature, Contract, E2E and
// Conformance tests and the helpers of tests/ they sign in with. A unit test
// of a Policy builds a Subject to ask Can, because there the Subject is the
// rule's input, and is not read.
//
// Severity. A warning until 2026-12-15.
//
// Positive: tests.SignedIn(t, app, auth.Subject{ID: "u1", Roles: []string{"admin"}});
// client.ActingAs(alice). One finding per literal and per ActingAs.
//
// Negative: a literal handed straight to a Policy's Can, or held in a variable
// every use of which is Can; a literal handed to the kit's As, which refuses
// anything beyond the account's ID and Tenant; an empty Subject, which is
// nobody; anything in tests/Unit.
//
// Known false positive: a literal handed to a helper that only asks Can.
//
// Limit. The function holding the literal: a variable is followed through the
// uses of its name there, and no further.
//
// Correction: test.As(t, server, auth.Subject{ID: user.ID, Tenant: user.Tenant}),
// with the role given to the account where the resolver reads it.
func testsSignInForReal(p *project) []Finding {
	var out []Finding
	why := "a subject written by the test reaches the handler past the guard and the SubjectResolver, so the roles it " +
		"carries are the test's and not the account's: the test passes while the screen is broken for the real account. " +
		"Sign in with test.As(t, server, auth.Subject{ID, Tenant}) and give the account its role where the resolver reads " +
		"it; a unit test of a Policy may build a Subject to ask Can." + promotion
	for _, f := range p.files {
		if !requestLayerFile(f) {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return false
				}
				if len(stack) > 0 {
					parents[n] = stack[len(stack)-1]
				}
				stack = append(stack, n)
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ActingAs" {
						out = append(out, Finding{
							Rule: "test-forged-subject", Severity: Warning,
							File: f.rel, Line: f.line(x),
							Message: "ActingAs puts the subject on the request without signing it in",
							Why:     why,
						})
					}
				case *ast.CompositeLit:
					if _, name, ok := f.importedName(x.Type, isSubjectPackage); !ok || name != "Subject" || len(x.Elts) == 0 {
						return true
					}
					if f.subjectOnlyAsked(x, parents, fn.Body) {
						return true
					}
					out = append(out, Finding{
						Rule: "test-forged-subject", Severity: Warning,
						File: f.rel, Line: f.line(x),
						Message: "this test writes the Subject a request runs as, instead of signing the account in",
						Why:     why,
					})
				}
				return true
			})
		}
	}
	return out
}

// subjectOnlyAsked reports whether a Subject literal is only ever the input of
// a Policy's Can or of the kit's As: directly, behind &, or through a variable
// every use of which is one of those.
func (f *file) subjectOnlyAsked(lit ast.Node, parents map[ast.Node]ast.Node, body *ast.BlockStmt) bool {
	node := lit
	if u, ok := parents[node].(*ast.UnaryExpr); ok && u.Op == token.AND {
		node = u
	}
	switch parent := parents[node].(type) {
	case *ast.CallExpr:
		return f.asksWith(parent)
	case *ast.AssignStmt:
		for i, rhs := range parent.Rhs {
			if rhs != node || i >= len(parent.Lhs) {
				continue
			}
			if id, ok := parent.Lhs[i].(*ast.Ident); ok {
				return f.variableOnlyAsked(id, parents, body)
			}
		}
	case *ast.ValueSpec:
		for i, v := range parent.Values {
			if v == node && i < len(parent.Names) {
				return f.variableOnlyAsked(parent.Names[i], parents, body)
			}
		}
	}
	return false
}

// variableOnlyAsked reports whether every use of the variable declared by id
// in body is an argument of Can or As.
func (f *file) variableOnlyAsked(id *ast.Ident, parents map[ast.Node]ast.Node, body *ast.BlockStmt) bool {
	used := false
	only := true
	ast.Inspect(body, func(n ast.Node) bool {
		use, ok := n.(*ast.Ident)
		if !ok || use == id || use.Name != id.Name {
			return true
		}
		var node ast.Node = use
		if u, ok := parents[node].(*ast.UnaryExpr); ok && u.Op == token.AND {
			node = u
		}
		call, ok := parents[node].(*ast.CallExpr)
		if !ok || !f.asksWith(call) {
			only = false
			return false
		}
		used = true
		return true
	})
	return used && only
}

// asksWith reports whether the call is a Policy's Can or the kit's As.
func (f *file) asksWith(call *ast.CallExpr) bool {
	if methodName(call) == "Can" {
		return true
	}
	imported, ok := f.importedFunction(callName(call))
	return ok && imported.name == "As" && kitPackage(imported.pkg, "")
}

// cssSelector reads like a selector: an id, a class or an attribute first, or
// a combinator or a structural pseudo-class anywhere.
var cssSelector = regexp.MustCompile(`^\s*[.#\[][A-Za-z_\-\[\]="']|\s>\s|:nth-|:first-child|:last-child`)

// locatorMethods are the calls of the browser harnesses applications wrote
// before the kit, and of the third-party drivers, that take a selector first.
var locatorMethods = map[string]bool{
	"Click": true, "Fill": true, "Type": true, "WaitVisible": true, "WaitReady": true, "WaitFor": true,
	"Find": true, "Locator": true, "QuerySelector": true, "QuerySelectorAll": true, "Hover": true,
	"Tap": true, "Focus": true, "MustElement": true, "Element": true, "SendKeys": true, "SetValue": true,
	"Text": true, "Exists": true, "Visible": true,
}

// kitLabelMethods are the page methods of the kit whose first argument is a
// label, which may begin with any character.
var kitLabelMethods = map[string]bool{"Fill": true, "Select": true, "Check": true, "Uncheck": true}

// 45. A journey finds an element the way the person does.
//
// Reason. A selector names the markup and not what the person sees: a class
// renamed for styling breaks the journey while the screen is unchanged, and a
// screen whose button lost its label passes with the class intact. The kit
// finds by role and accessible name, label, visible text and data-testid, and
// keeps a CSS locator only with the reason nothing else can say it.
//
// Scope. tests/E2E and tests/Browser.
//
// Severity. A warning until 2026-12-15.
//
// Positive: browser.CSS(".pay", ""); page.Click("#submit") of a harness;
// document.querySelector inside a script the test evaluates. One finding per
// call or literal.
//
// Negative: browser.CSS("canvas.chart", "the chart has no accessible name");
// page.ClickText("Pay"); page.Fill("Card number", "4242"); page.Fill("#tag",
// "urgent") of the kit, whose "#tag" is a label.
//
// Known false positive: a text that starts with a dot or a hash handed to a
// harness method named like a locator, when it is a label. The kit's Fill,
// Select, Check and Uncheck take a label, and are not read as selectors in a
// file that imports the kit's browser package.
//
// Limit. String literals at the call; a selector held in a variable is not
// followed.
//
// Correction: page.ClickRole("button", "Pay"), page.Fill("Card number", v),
// browser.TestID("pay"); or browser.CSS with the reason.
func locatorsSpeakAsTheUser(p *project) []Finding {
	var out []Finding
	why := "a selector names the markup and not what the person sees: a class renamed for styling breaks the journey while " +
		"the screen is unchanged, and a button that lost its label passes. Find it by role and name, label, text or " +
		"data-testid; keep a CSS locator only as browser.CSS with the reason nothing else can say it." + promotion
	for _, f := range p.files {
		if !journeyFile(f) {
			continue
		}
		usesKit := false
		for imported := range f.imports {
			if kitPackage(imported, "/browser") {
				usesKit = true
			}
		}
		f.calls(func(call *ast.CallExpr, _ string) {
			if imported, ok := f.importedFunction(callName(call)); ok && imported.name == "CSS" && kitPackage(imported.pkg, "/browser") {
				if reason, ok := stringArg(call, 1); ok && strings.TrimSpace(reason) == "" {
					out = append(out, Finding{
						Rule: "test-css-locator", Severity: Warning,
						File: f.rel, Line: f.line(call),
						Message: "this journey finds an element by CSS with no reason given",
						Why:     why,
					})
				}
				return
			}
			if !locatorMethods[methodName(call)] || (usesKit && kitLabelMethods[methodName(call)]) {
				return
			}
			if s, ok := stringArg(call, 0); ok && cssSelector.MatchString(s) {
				out = append(out, Finding{
					Rule: "test-css-locator", Severity: Warning,
					File: f.rel, Line: f.line(call),
					Message: "this journey finds an element by the CSS selector " + strconv.Quote(s),
					Why:     why,
				})
			}
		})
		ast.Inspect(f.ast, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !strings.Contains(lit.Value, "querySelector") {
				return true
			}
			out = append(out, Finding{
				Rule: "test-css-locator", Severity: Warning,
				File: f.rel, Line: f.line(lit),
				Message: "this journey selects an element with querySelector inside a script",
				Why:     why,
			})
			return true
		})
	}
	return out
}

// copiedConformanceDirs are where the framework's authentication suites were
// copied into applications.
var copiedConformanceDirs = []string{"tests/AuthContracts/", "tests/AuthSchema/", "tests/NativeAuth/", "tests/NativeAuthSession/"}

// copiedConformanceTests are the tests of those copies, by name.
var copiedConformanceTests = map[string]bool{
	"TestNativeAuthenticationDoesNotImportTheCommunityModule":         true,
	"TestAuthControllerPlaceholderUsesTheInstallerPackage":            true,
	"TestApplicationCodeDoesNotNameSessionGuard":                      true,
	"TestTheAccountRulesAreNotCopiedIntoTheApplication":               true,
	"TestTheApplicationOwnsTheExactNativeAuthMigrationSubset":         true,
	"TestTheNativeAuthCatalogAllowsAdditionalDomainMigrations":        true,
	"TestFreshNativeAuthMigrationsCreateTheExactPortableSchema":       true,
	"TestTheUsersSchemaIsolatesEmailUniquenessByTenant":               true,
	"TestAnExistingAuthSchemaUpgradesOnceAndRollsBackOnlyTheNewBatch": true,
	"TestAccountContract":                                   true,
	"TestRolesAreOneValueInGoAndJSONInTheColumn":            true,
	"TestARegistrationCannotGrantItselfARole":               true,
	"TestTheUserPolicyRefusesAPublicNameReadWithoutAReader": true,
	"TestRegistrationPersistsSQLNullUntilEmailIsVerified":   true,
	"TestSeededVerifiedUserPreservesVerificationContracts":  true,
	"TestPasswordAndPendingFactorSessionMatrix":             true,
}

// 46. The framework's suites are called, not copied.
//
// Reason. The authentication suites were copied into every application, and
// the copies drifted: a migration behind, a shape the framework has since
// changed, a case one copy has and another lacks. The kit is written once and
// takes what is the application's as parameters, so a call proves what the
// framework proves today.
//
// Scope. Test files under tests/AuthContracts, tests/AuthSchema,
// tests/NativeAuth and tests/NativeAuthSession, and any test file declaring
// one of the copied suites' tests by name.
//
// Severity. A warning until 2026-12-15.
//
// Positive: tests/AuthContracts/Boundaries_test.go declaring
// TestApplicationCodeDoesNotNameSessionGuard. One finding per file, at its
// first copied test, or at its package clause.
//
// Negative: tests/Conformance/Auth_test.go calling conformance.Boundaries;
// a file in one of those directories that imports the conformance kit.
//
// Known false positive: an application's own test that reuses one of the
// names.
//
// Limit. Directory and function names; a copy renamed and moved is not
// recognised.
//
// Correction: one line per kit in tests/Conformance --
// conformance.Boundaries(t, "../.."), Schema, Account, ModelPersistence,
// SessionMatrix, Redaction -- and delete the copy.
func conformanceIsCalledNotCopied(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !f.isTest || !strings.HasPrefix(f.rel, "tests/") {
			continue
		}
		callsTheKit := false
		for imported := range f.imports {
			if kitPackage(imported, "/conformance") {
				callsTheKit = true
			}
		}
		if callsTheKit {
			continue
		}
		inCopy := false
		for _, dir := range copiedConformanceDirs {
			if strings.HasPrefix(f.rel, dir) {
				inCopy = true
			}
		}
		at, named := f.line(f.ast.Name), ""
		for _, decl := range f.ast.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && copiedConformanceTests[fn.Name.Name] {
				at, named = f.line(fn), fn.Name.Name
				break
			}
		}
		if !inCopy && named == "" {
			continue
		}
		message := "this file is a copy of the framework's authentication suite"
		if named != "" {
			message += " (" + named + ")"
		}
		out = append(out, Finding{
			Rule: "test-copied-conformance", Severity: Warning,
			File: f.rel, Line: at,
			Message: message,
			Why: "the copies in each application drift -- a migration behind, a shape the framework has since changed -- and " +
				"each proves something different. Call the kit in one line from tests/Conformance: conformance.Boundaries(t, " +
				"\"../..\"), Schema, Account, ModelPersistence, SessionMatrix and Redaction from " +
				"github.com/arandu-io/framework/test/conformance, and delete the copy." + promotion,
		})
	}
	return out
}

// ciFiles are the places a CI service reads its pipeline from, as globs from
// the project root.
var ciFiles = []string{
	".github/workflows/*.yml", ".github/workflows/*.yaml", ".gitlab-ci.yml", ".circleci/config.yml",
	"bitbucket-pipelines.yml", ".drone.yml", ".woodpecker.yml", ".woodpecker/*.yml", ".woodpecker/*.yaml",
	"azure-pipelines.yml", ".buildkite/*.yml", ".buildkite/*.yaml", "Jenkinsfile",
}

// runsTheJourneys matches a line that runs the end-to-end tests: aru test with
// --e2e, --smoke or the old --browser, or go test with the e2e tag (or the old
// browser tag).
var (
	aruRunsJourneys = regexp.MustCompile(`\baru\s+test\b.*(--e2e|--smoke|--browser)`)
	goRunsJourneys  = regexp.MustCompile(`\bgo\s+test\b.*-tags[ =]["']?[^\s"']*\b(e2e|browser)\b`)
)

// 47. The journeys run in CI.
//
// Reason. A journey carries the e2e build tag, so go test ./... does not even
// compile it; a pipeline that runs only that, or that only vets the tag,
// leaves the screens the journeys guard to break unseen.
//
// Scope. A project with a test file under tests/E2E (or tests/Browser), and
// the CI pipelines at the root: GitHub Actions, GitLab, CircleCI, Bitbucket,
// Drone, Woodpecker, Azure, Buildkite and a Jenkinsfile.
//
// Severity. A warning until 2026-12-15.
//
// Positive: tests/E2E with no pipeline at all; a workflow whose only mention
// of the tag is go vet -tags e2e. One finding, at the first journey.
//
// Negative: a workflow line running aru test --smoke, aru test --e2e, or go
// test -tags e2e; a project with no journeys.
//
// Known false positive: a pipeline kept outside the repository, or one that
// calls a script which runs the journeys.
//
// Limit. The pipeline files above, read as lines; a script a step calls is not
// read.
//
// Correction: run aru test --smoke on every pull request and aru test --e2e
// at night.
func journeysRunInCI(p *project) []Finding {
	var first *file
	for _, f := range p.files {
		if f.isTest && journeyFile(f) && (first == nil || f.rel < first.rel) {
			first = f
		}
	}
	if first == nil {
		return nil
	}
	var pipelines []string
	for _, glob := range ciFiles {
		found, _ := filepath.Glob(filepath.Join(p.root, filepath.FromSlash(glob)))
		pipelines = append(pipelines, found...)
	}
	sort.Strings(pipelines)
	for _, path := range pipelines {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if aruRunsJourneys.MatchString(line) || goRunsJourneys.MatchString(line) {
				return nil
			}
		}
	}
	return []Finding{{
		Rule: "e2e-not-run-in-ci", Severity: Warning,
		File: first.rel, Line: 1,
		Message: "the journeys in " + filepath.ToSlash(filepath.Dir(first.rel)) + " run in no CI pipeline",
		Why: "a journey carries the e2e build tag, so go test ./... does not compile it, and the screen it guards breaks " +
			"unseen. Run `aru test --smoke` on every pull request and `aru test --e2e` at night; go vet -tags e2e only " +
			"compiles them." + promotion,
	}}
}

// 48. A skipped test says why.
//
// Reason. A skip with no reason reads like a pass in a summary, and nobody can
// tell whether the environment, a bug or a forgotten line switched the test
// off; in CI a test that skips is a test that never runs.
//
// Scope. Every _test.go file, and every file of tests/.
//
// Severity. A warning until 2026-12-15.
//
// Positive: t.Skip(), t.SkipNow(), t.Skip(""), t.Skipf(""). One finding per
// call.
//
// Negative: t.Skip("needs Postgres: set ARANDU_TEST_DATABASE_URL");
// t.Skipf("no browser: %v", err).
//
// Known false positive: a method called Skip on something that is not a test.
//
// Limit. The call's own arguments; a reason held in a variable counts as one.
//
// Correction: say why, or remove the skip.
func skipsSayWhy(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if !suiteFile(f) {
			continue
		}
		f.calls(func(call *ast.CallExpr, _ string) {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return
			}
			bare := false
			switch sel.Sel.Name {
			case "SkipNow":
				bare = true
			case "Skip", "Skipf":
				if len(call.Args) == 0 {
					bare = true
				} else if s, ok := stringArg(call, 0); ok && strings.TrimSpace(s) == "" {
					bare = true
				}
			}
			if !bare {
				return
			}
			out = append(out, Finding{
				Rule: "skip-without-reason", Severity: Warning,
				File: f.rel, Line: f.line(call),
				Message: "this test skips without saying why",
				Why: "a skip with no reason reads like a pass in a summary, and nobody can tell whether the environment, a bug or " +
					"a forgotten line switched it off; in CI a test that skips never runs. Say why -- t.Skip(\"needs Postgres: " +
					"set ARANDU_TEST_DATABASE_URL\") -- or remove the skip." + promotion,
			})
		})
	}
	return out
}

// driverPackages are the import paths of browser drivers: the kit's native one
// under each spelling, the Playwright module, and the third-party libraries.
func driverPackage(path string) bool {
	if kitPackage(path, "/browser/cdp") || strings.HasPrefix(path, "github.com/hyz-is/arandu-playwright") {
		return true
	}
	for _, driver := range browserDrivers {
		if strings.HasPrefix(path, driver) {
			return true
		}
	}
	return false
}

// 49. A test drives the page, never one driver.
//
// Reason. The kit's page is written once over a small driver interface, so
// the same journeys run on the native driver in a pull request and on
// Playwright's engines at night. A test that imports one driver, or reaches
// through the page to one driver's type, runs on that driver alone.
//
// Scope. Every _test.go file, and every file under a directory of tests/; the
// files directly in tests/ are the test case, where the Registry names the
// drivers once.
//
// Severity. A warning until 2026-12-15.
//
// Positive: tests/E2E/Checkout_test.go importing
// github.com/arandu-io/framework/test/browser/cdp or github.com/chromedp/chromedp;
// page.Driver().(*cdp.Driver). One finding per import and per assertion.
//
// Negative: tests/TestCase.go writing browser.Registry{"cdp": cdp.Launch};
// a journey importing only the browser package.
//
// Known false positive: the driver's own conformance test, in a driver
// module.
//
// Limit. Imports and type assertions on a Driver() call; an interface method
// called through a variable is not followed.
//
// Correction: drive the page through the browser package and name the drivers
// in the Registry of tests/TestCase.go.
func testsNameNoDriver(p *project) []Finding {
	var out []Finding
	why := "a test that calls one driver's API runs on that driver alone, and the suite can no longer run on the native " +
		"driver in a pull request and on Playwright at night. Drive the page through the browser package and name the " +
		"drivers once, in the Registry of tests/TestCase.go." + promotion
	for _, f := range p.files {
		testCase := strings.HasPrefix(f.rel, "tests/") && !strings.Contains(strings.TrimPrefix(f.rel, "tests/"), "/") && !f.isTest
		if testCase || !suiteFile(f) {
			continue
		}
		for _, imp := range f.ast.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !driverPackage(path) {
				continue
			}
			out = append(out, Finding{
				Rule: "driver-specific-api-in-test", Severity: Warning,
				File: f.rel, Line: f.line(imp),
				Message: "this test imports " + path + ", one browser driver's API",
				Why:     why,
			})
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			assert, ok := n.(*ast.TypeAssertExpr)
			if !ok || assert.Type == nil {
				return true
			}
			call, ok := assert.X.(*ast.CallExpr)
			if !ok || methodName(call) != "Driver" {
				return true
			}
			out = append(out, Finding{
				Rule: "driver-specific-api-in-test", Severity: Warning,
				File: f.rel, Line: f.line(assert),
				Message: "this test reaches through the page to one driver's type",
				Why:     why,
			})
			return true
		})
	}
	return out
}
