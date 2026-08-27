package doctor

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestEveryRuleFiresOnAFixture walks the rule set and demands that each one
// produce at least one finding across the fixtures.
//
// It guards against one shape of failure: a rule is believed to cover something
// it never covered, and nobody checks that the check exists. Without this, a
// rule can be deleted whole and the suite stays green.
//
// A rule that fires on nothing is indistinguishable from a rule that was
// deleted, and the only difference is how long it takes to find out.
func TestEveryRuleFiresOnAFixture(t *testing.T) {
	fired := map[string]bool{}
	for _, dir := range []string{"testdata/violations", "testdata/gaps", "testdata/broken"} {
		findings, err := Run(dir, Conventional)
		if err != nil {
			continue // a fixture that does not load is the other tests' problem
		}
		for _, f := range findings {
			fired[f.Rule] = true
		}
	}
	// The performance profile has to be walked too, or the three rules that only
	// run there are indistinguishable from three rules somebody deleted. The
	// fixture is the clean one on purpose: what it holds is correct code with a
	// join in it, which is exactly what the profile turns into a finding.
	performance, err := Run("testdata/clean", Performance)
	if err != nil {
		t.Fatalf("Run on the performance profile: %v", err)
	}
	for _, f := range performance {
		fired[f.Rule] = true
	}

	// A rule reached by no fixture is named by its function, which is what the
	// person who has to write the fixture needs to know.
	var silent []string
	for _, rule := range rules {
		name := runtime.FuncForPC(reflect.ValueOf(rule).Pointer()).Name()
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if !firedFor(name, fired) {
			silent = append(silent, name)
		}
	}

	// And the other direction: a rule DELETED from the slice does not appear in
	// the loop above, so it disappears without leaving a trace.
	//
	// The emits map is the list of what has to exist. Removing a rule from the
	// slice without removing it from the map fails here, and removing it from
	// both is an explicit decision -- which is what deleting a security rule
	// should look like in a diff.
	declared := map[string]bool{}
	for _, rule := range rules {
		name := runtime.FuncForPC(reflect.ValueOf(rule).Pointer()).Name()
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		declared[name] = true
	}
	for fn := range emitsByRule() {
		if !declared[fn] {
			t.Errorf("%s is written down as a rule and is not in the rules slice: it was removed, and nothing else noticed", fn)
		}
	}

	if len(silent) > 0 {
		t.Errorf("these rules produced no finding on any fixture, so nothing proves they still work:\n  %s\n\nfired: %s",
			strings.Join(silent, "\n  "), strings.Join(keys(fired), ", "))
	}
}

// firedFor maps a rule function's name to the finding names it can produce.
//
// The mapping is written out rather than derived, because one function can emit
// more than one rule name and the compiler cannot tell which. Adding a rule
// without adding it here fails the test below, which is the point.
func firedFor(fn string, fired map[string]bool) bool {
	names, known := emitsByRule()[fn]
	if !known {
		// A rule added to the slice and not to the map. Reporting it as silent
		// is right: nobody wrote down what it emits.
		return false
	}
	for _, n := range names {
		if fired[n] {
			return true
		}
	}
	return false
}

// emitsByRule is what each rule can report.
//
// Written out rather than derived, because one function can emit more than one
// name and the compiler cannot tell which. It is also the list of rules that
// must exist -- see the inverse check above.
func emitsByRule() map[string][]string {
	return map[string][]string{
		"unreadableFiles":                         {"file-does-not-parse"},
		"repositoryNeedsPolicy":                   {"repository-without-policy"},
		"repositoryMethodNeedsGrant":              {"grant-not-checked", "grant-not-received", "grant-check-discarded"},
		"policyMustBeOpened":                      {"policy-never-opened"},
		"controllerMustNotReachData":              {"handler-reaches-data", "handler-reaches-the-model"},
		"controllerMustNotReachTheRepository":     {"controller-reaches-repository"},
		"tenantMustComeFromTheGrant":              {"tenant-from-request", "tenant-from-header"},
		"systemGrantIsAudited":                    {"system-grant-without-tenant", "system-grant-outside-scope"},
		"noBuiltSQL":                              {"sql-built-with-sprintf", "sql-built-by-concatenation"},
		"sensitiveFieldNeedsRedaction":            {"sensitive-field-not-redacted"},
		"sessionMustRotateOnLogin":                {"session-not-rotated"},
		"viewDataMustBeAStruct":                   {"view-data-is-a-map"},
		"viewMustExist":                           {"view-does-not-exist"},
		"declaredPermissionsMatchTheCode":         {"permission-not-declared", "permission-not-used"},
		"viewsKeepNoStateInTheBrowser":            {"view-keeps-state-in-the-browser"},
		"tenantMustScopeTheSQL":                   {"sql-without-tenant-scope"},
		"theOutboxTableTravelsWithWhatWritesToIt": {"outbox-not-registered"},
		"resourceNotReauthorized":                 {"resource-not-reauthorized"},
		"rawOutputIsAComponent":                   {"raw-output-is-not-a-component"},
		"noRetiredModuleIsImported":               {"retired-module"},
		"testsAreWhereTheyCanRun": {
			"test-is-not-run", "test-outside-the-tests-tree",
			"package-clause-is-capitalised", "scaffolding-ships",
		},
		"migrationsMustReachTheBinary":       {"migrations-not-linked"},
		"addedColumnsMustBeNullable":         {"added-column-not-nullable"},
		"migrationsMustBeReversible":         {"rollback-does-nothing"},
		"theProfileIsDeclared":               {"profile-not-declared"},
		"queriesReachOneAggregate":           {"join-across-aggregates"},
		"transactionsStayInsideOneAggregate": {"transaction-across-aggregates"},
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestTheDocumentedRuleCountIsTheOneThisPackageHas checks the figures this
// repository's documents quote against the rule set they describe.
//
// The count has gone stale eight times, and the last one failed differently in
// kind: the number was wrong because every instrument that had ever measured it
// read rules.go alone. Four of the names are declared in internal/testlayout and
// forwarded by testsAreWhereTheyCanRun, so no grep over that one file could
// reach them however long it was left in place -- and a figure with a command
// beside it reads as verified whether or not the command can answer.
//
// So the four figures are derived here, from the rules slice, from emitsByRule
// and from both files that declare a name, and then read back out of every
// document that quotes one. Nothing below is written down twice. A rule added or
// deleted fails this test in the run that adds or deletes it, and the failure
// names the document and the figure, which is the only arrangement in which
// these cannot drift apart again.
func TestTheDocumentedRuleCountIsTheOneThisPackageHas(t *testing.T) {
	const (
		skill  = "../../.agents/skills/aru-doctor-rule/SKILL.md"
		agents = "../../AGENTS.md"
		readme = "../../README.md"

		rulesFile  = "rules.go"
		layoutFile = "../testlayout/testlayout.go"
	)

	own := declaredRuleNames(t, rulesFile)
	forwarded := declaredRuleNames(t, layoutFile)
	if len(own) == 0 || len(forwarded) == 0 {
		t.Fatal("one of the two files declares no rule name at all, so every figure below measures nothing")
	}

	// The registry and the two sources have to name one set. Either half
	// disagreeing is what makes a figure that looks measured wrong: a name in
	// the source and not in emitsByRule is a rule no fixture proves fires, and a
	// name in emitsByRule that neither file declares is one that no longer
	// exists.
	total := map[string]bool{}
	for _, names := range emitsByRule() {
		for _, name := range names {
			total[name] = true
		}
	}
	for name := range own {
		if !total[name] {
			t.Errorf("%s declares %s and no rule in emitsByRule reports it", rulesFile, name)
		}
	}
	for name := range forwarded {
		if own[name] {
			t.Errorf("%s is declared in both files, so the figures below count it twice", name)
		}
		if !total[name] {
			t.Errorf("%s declares %s and no rule in emitsByRule forwards it", layoutFile, name)
		}
	}
	for name := range total {
		if !own[name] && !forwarded[name] {
			t.Errorf("emitsByRule reports %s and neither file declares it", name)
		}
	}

	figures := map[string]int{
		"rule functions":      len(rules),
		"names in total":      len(total),
		"names declared here": len(own),
		"names forwarded":     len(forwarded),
	}

	// Each pattern anchors one sentence in one document. They are written as
	// expressions rather than as the figure so that what fails is the sentence
	// that is wrong, named, rather than a count somebody then has to go looking
	// for.
	for _, quote := range []struct {
		path, pattern, figure string
	}{
		{skill, `#\s*(\d+)\s+rule functions`, "rule functions"},
		{skill, `#\s*(\d+)\s+names a report can carry`, "names in total"},
		{skill, `#\s*(\d+)\s+of them declared here`, "names declared here"},
		{skill, `#\s*(\d+)\s+forwarded, declared there`, "names forwarded"},

		{agents, `(\d+) rule functions reading a project's parsed AST`, "rule functions"},
		{agents, `emitting (\d+) rule names of their own`, "names declared here"},
		{agents, `rule names of their own and (\d+) borrowed`, "names forwarded"},
		{agents, `testlayout\.go \| sort -u \| wc -l\s+#\s*(\d+)`, "names in total"},

		{readme, `(\d+) named rules read the AST`, "names in total"},
	} {
		got, ok := quoted(t, quote.path, quote.pattern)
		if !ok {
			continue
		}
		if want := figures[quote.figure]; got != want {
			t.Errorf("%s says %d %s and this package has %d: correct the document",
				quote.path, got, quote.figure, want)
		}
	}
}

// declaredRuleNames reads the rule names one file declares, with the expression
// the documented commands match on.
func declaredRuleNames(t *testing.T, path string) map[string]bool {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`Rule: *"([a-z0-9-]+)"`).FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	return out
}

// quoted answers the one number a document states for one figure.
//
// Exactly one match is required, and that is the half worth writing down: a
// pattern matching twice is an anchor that has stopped identifying a single
// sentence, and reading whichever came first is how a guard goes on passing over
// the line it no longer looks at. Matching nothing is the same failure from the
// other side -- the sentence was reworded and the guard silently stopped
// guarding it.
func quoted(t *testing.T, path, pattern string) (int, bool) {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("reading %s: %v", path, err)
		return 0, false
	}

	found := regexp.MustCompile(pattern).FindAllStringSubmatch(string(body), -1)
	switch len(found) {
	case 1:
		n, err := strconv.Atoi(found[0][1])
		if err != nil {
			t.Errorf("%s: %q is not a number in %s", path, found[0][1], pattern)
			return 0, false
		}
		return n, true
	case 0:
		t.Errorf("nothing in %s matches %s, so no figure there is being checked any more", path, pattern)
	default:
		t.Errorf("%d places in %s match %s, so the figure is written more than once and the anchor names none of them",
			len(found), path, pattern)
	}
	return 0, false
}

// TestEveryGrantConstructorIsGuarded compares grantConstructors against what the
// two modules actually export.
//
// The list is written by hand because the doctor reads one package at a time and
// does not resolve types across modules -- it cannot ask "what returns a Grant"
// the way a compiler can. A hand-written list is exactly what went wrong: the
// rules matched `security.SystemGrant` and `jobs.GrantFor` wrapped it, so a Grant
// for an action and a tenant nobody authorized produced no finding at all.
//
// Both modules are read, and that is the correction this test needed itself. It
// looked only at the framework, and the framework re-exports two functions that
// hesape declares -- so hesape/auth.SystemGrant and hesape/queue/jobs.GrantFor
// were invisible to the check that exists to find exactly this. A project
// reaches either module directly.
//
// Authorize is excluded on purpose, in both: it is the mandatory path, and a
// Grant that came from it was authorized by a Policy.
//
// A module that is not on disk is skipped rather than passed over quietly, and
// finding nothing anywhere is a failure -- a guard that silently does nothing is
// the thing it exists to prevent.
func TestEveryGrantConstructorIsGuarded(t *testing.T) {
	// `func Name(...) [pkg.]Grant` and `... (Grant, error)`, exported only.
	//
	// The qualifier is any package and not `security.` alone. hesape spells the
	// return type `auth.Grant`, and a regexp that knew one qualifier read every
	// constructor in that module as no constructor at all.
	decl := regexp.MustCompile(`^func ([A-Z]\w*)(\[[^]]*\])?\([^)]*\)\s*\(?([a-z]\w*\.)?Grant\b`)

	found := map[string]string{} // import path + symbol -> file
	var read int
	for _, module := range []string{"framework", "hesape"} {
		root := filepath.Join("..", "..", "..", module)
		if _, err := os.Stat(root); err != nil {
			t.Logf("%s is not checked out next to this repository", module)
			continue
		}
		read++

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			pkg, err := importPathOf(filepath.Dir(path), root)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(body), "\n") {
				if m := decl.FindStringSubmatch(line); m != nil {
					found[pkg+"."+m[1]] = path
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if read == 0 {
		t.Skip("neither module is checked out next to this repository")
	}
	if len(found) == 0 {
		t.Fatal("no Grant constructor found in either module: this test stopped testing anything")
	}

	guarded := map[string]bool{}
	for _, c := range grantConstructors {
		guarded[c.pkg+"."+c.name] = true
	}
	// The mandatory path, in both modules. A Grant it returns was authorized.
	guarded["github.com/arandu-io/framework/security.Authorize"] = true
	guarded["github.com/arandu-io/hesape/auth.Authorize"] = true

	for symbol, path := range found {
		if !guarded[symbol] {
			t.Errorf("%s (%s) hands back a Grant and no rule watches it: add it to grantConstructors, "+
				"and give it the shape it spells the tenant in", symbol, path)
		}
	}
}

// TestAGrantConstructorIsResolvedThroughTheImportBlock pins the decision that
// makes the rule survive an alias.
//
// The identifier at the call site belongs to whoever wrote the file. Matching it
// as text means the rule is off for anyone who renames an import, and on by
// coincidence for anyone who does not -- and the components moved into hesape
// under packages whose last path element is unchanged, so the coincidence was
// holding two of these four up.
func TestAGrantConstructorIsResolvedThroughTheImportBlock(t *testing.T) {
	const (
		fwSecurity = "github.com/arandu-io/framework/security"
		fwJobs     = "github.com/arandu-io/framework/jobs"
		hAuth      = "github.com/arandu-io/hesape/auth"
		hJobs      = "github.com/arandu-io/hesape/queue/jobs"
	)

	for _, c := range []struct {
		imports map[string]string
		called  string
		want    string // the constructor's function name, or "" for no match
		why     string
	}{
		{map[string]string{fwSecurity: "security"}, "security.SystemGrant", "SystemGrant", "the spelling everybody writes"},
		{map[string]string{fwJobs: "jobs"}, "jobs.GrantFor", "GrantFor", "the wrapper, unaliased"},

		{map[string]string{hJobs: "hjobs"}, "hjobs.GrantFor", "GrantFor", "the alias that switched the rule off"},
		{map[string]string{hAuth: "auth"}, "auth.SystemGrant", "SystemGrant", "hesape declares it and the list did not name it"},
		{map[string]string{hAuth: "a"}, "a.SystemGrant", "SystemGrant", "one letter is a legal import name"},
		{map[string]string{hJobs: "jobs"}, "jobs.GrantFor", "GrantFor", "hesape under the name the framework's package also has"},
		{map[string]string{fwSecurity: "."}, "SystemGrant", "SystemGrant", "a dot import qualifies nothing at all"},

		{map[string]string{fwSecurity: "security"}, "security.Authorize", "", "the mandatory path is not an escape hatch"},
		{map[string]string{"example.test/p/jobs": "jobs"}, "jobs.GrantFor", "", "the project's own package, sharing a name"},
		{map[string]string{fwJobs: "jobs"}, "q.jobs.GrantFor", "", "a field chain is not a package call"},
		{map[string]string{}, "jobs.GrantFor", "", "an identifier this file never imported"},
		{map[string]string{fwSecurity: "security"}, "SystemGrant", "", "unqualified, and nothing was dot-imported"},
	} {
		f := &file{imports: c.imports}
		got, matched := f.grantConstructor(c.called)
		if matched != (c.want != "") {
			t.Errorf("grantConstructor(%q) with imports %v matched = %t, want %t: %s",
				c.called, c.imports, matched, c.want != "", c.why)
			continue
		}
		if got.name != c.want {
			t.Errorf("grantConstructor(%q) = %q, want %q: %s", c.called, got.name, c.want, c.why)
		}
	}
}

// importPathOf is the import path of a directory inside a checked-out module.
//
// The module path comes from the nearest go.mod at or above the directory, not
// from the one at the root: hesape publishes six submodules of its own, and
// naming a package of one of them after the root module produces a path that
// resolves to nothing.
func importPathOf(dir, root string) (string, error) {
	at := dir
	for {
		body, err := os.ReadFile(filepath.Join(at, "go.mod"))
		if err == nil {
			module := ""
			for _, line := range strings.Split(string(body), "\n") {
				if rest, cut := strings.CutPrefix(strings.TrimSpace(line), "module "); cut {
					module = strings.TrimSpace(rest)
					break
				}
			}
			if module == "" {
				return "", fmt.Errorf("%s/go.mod declares no module path", at)
			}
			rel, err := filepath.Rel(at, dir)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return module, nil
			}
			return module + "/" + filepath.ToSlash(rel), nil
		}
		parent := filepath.Dir(at)
		if at == root || parent == at {
			return "", fmt.Errorf("no go.mod at or above %s", dir)
		}
		at = parent
	}
}

// TestIsNamedCallSeparatesAComponentFromAValue pins the one decision the raw
// interpolation rule makes.
//
// The passing shapes are the ones measured across the views that exist: a
// package-qualified component, a component from the same package, and a call
// with no arguments. The failing ones are what the rule exists to find, plus the
// two ways a reader would expect it to be fooled -- a parenthesis inside a
// label, and an expression that opens with a call and does not end with one.
func TestIsNamedCallSeparatesAComponentFromAValue(t *testing.T) {
	for _, c := range []struct {
		expr string
		want bool
		why  string
	}{
		{"components.ThemeToggle()", true, "a component with no arguments"},
		{"components.Field(components.FieldProps{Name: \"email\"})", true, "the shape most raw interpolations have"},
		{"Badge(BadgeProps{Label: x})", true, "a component from the same package"},
		{"icons.Tag(icons.Props{Label: \"Close (esc)\"})", true, "a parenthesis inside a string literal is not a parenthesis"},
		{"mailui.Layout(mailui.LayoutProps{\n\tBrand: .BrandName,\n})", true, "a call the parser joined from several lines"},

		{".Body", false, "a field of the page data, which is a value"},
		{".Invoice.Notes", false, "a field one level down is still a value"},
		{"body", false, "a local variable is a value"},
		{"\"<b>x</b>\"", false, "a string literal is a value, however it was written"},
		{"", false, "nothing at all"},
		{"components.Alert(x) + .Body", false, "it opens with a call and the page gets the half after the plus"},
		{".Body + components.Alert(x)", false, "the same, with the halves swapped"},
		{"components.Alert(x", false, "an argument list that never closes"},
		{"icons.Tag(\"unterminated", false, "a literal that never closes"},
	} {
		if got := isNamedCall(c.expr); got != c.want {
			t.Errorf("isNamedCall(%q) = %t, want %t: %s", c.expr, got, c.want, c.why)
		}
	}
}

// TestRetiredModuleForMatchesThePathElementAndNotThePrefix pins the one
// decision the retired-module rule makes.
//
// Both ways of getting it wrong cost more than having no rule at all: matching
// too little leaves the subpackages of a deleted repository unreported, which
// is most of the imports anybody writes; matching too much tells somebody their
// own module was deleted, and a report with an invented finding in it is a
// report the next person skims.
func TestRetiredModuleForMatchesThePathElementAndNotThePrefix(t *testing.T) {
	for _, c := range []struct {
		path  string
		want  string
		moved string
		why   string
	}{
		{"github.com/arandu-io/queue", "github.com/arandu-io/queue", "github.com/arandu-io/hesape/queue", "the module root"},
		{"github.com/arandu-io/queue/kv", "github.com/arandu-io/queue", "github.com/arandu-io/hesape/queue", "a subpackage is as gone as the root"},
		{"github.com/arandu-io/database/postgres", "github.com/arandu-io/database", "github.com/arandu-io/hesape/database", "the same, one repository over"},
		{"github.com/arandu-io/kv", "github.com/arandu-io/kv", "github.com/arandu-io/hesape/redis", "the destination is not named after the source"},
		{"github.com/arandu-io/storage", "github.com/arandu-io/storage", "github.com/arandu-io/hesape/filesystem", "nor here"},

		{"github.com/arandu-io/queuebase", "", "", "somebody else's module that starts with the same letters"},
		{"github.com/arandu-io/framework/jobs", "", "", "the bridge has a removal date and is what make:job writes today"},
		{"github.com/arandu-io/hesape/queue", "", "", "the destination is not the thing being reported"},
		{"github.com/arandu-io/framework/data", "", "", "the framework did not move"},
		{"database/sql", "", "", "the standard library shares a word with one of them"},
		{"example.test/project/app/Repositories", "", "", "a package of the project under check"},
	} {
		retired, moved, ok := retiredModuleFor(c.path)
		if ok != (c.want != "") {
			t.Errorf("retiredModuleFor(%q) matched = %t, want %t: %s", c.path, ok, c.want != "", c.why)
			continue
		}
		if retired != c.want || moved != c.moved {
			t.Errorf("retiredModuleFor(%q) = %q, %q; want %q, %q: %s", c.path, retired, moved, c.want, c.moved, c.why)
		}
	}
}

// TestTablesNamedSeparatesAJoinFromASubquery pins the one decision the aggregate
// rules make.
//
// Everything else about the performance profile follows from this function: a
// statement that names two tables is a join and cannot be spelled on a
// wide-column store, and a statement that names one is the shape the generator
// emits. Getting it wrong in the noisy direction is worse than having no rule,
// because whoever trusts it redesigns a write that was already correct.
func TestTablesNamedSeparatesAJoinFromASubquery(t *testing.T) {
	for _, c := range []struct {
		sql  string
		want []string
		why  string
	}{
		{"SELECT id FROM invoices WHERE tenant_id = ?", []string{"invoices"}, "the ordinary read"},
		{"UPDATE invoices SET total = ? WHERE id = ?", []string{"invoices"}, "the ordinary write"},
		{"DELETE FROM invoices WHERE id = ?", []string{"invoices"}, "the ordinary delete"},
		{"INSERT INTO invoices (id, total) VALUES (?, ?)", []string{"invoices"}, "the column list is not a table"},

		// The generated List, as sqlStatements sees it: the columns constant is
		// not a literal and drops out, and the cursor predicate reads the
		// repository's own table twice.
		{"SELECT  FROM invoices WHERE tenant_id = ?", []string{"invoices"}, "the generated List"},
		{"AND ( created_at > (SELECT created_at FROM invoices WHERE id = ? AND tenant_id = ?) OR (created_at = (SELECT created_at FROM invoices WHERE id = ? AND tenant_id = ?) AND id > ?))",
			[]string{"invoices"}, "the keyset cursor: a subquery over one aggregate, which every generated module carries"},

		{"SELECT i.id FROM invoices i JOIN customers c ON c.id = i.customer_id", []string{"invoices", "customers"}, "the join, with aliases"},
		{"SELECT id FROM invoices, lines WHERE lines.invoice_id = invoices.id", []string{"invoices", "lines"}, "a join written without the word"},
		{"SELECT id FROM invoices LEFT OUTER JOIN lines ON lines.invoice_id = invoices.id", []string{"invoices", "lines"}, "the word is still JOIN however it is qualified"},
		{"INSERT INTO summaries (id) SELECT id FROM invoices", []string{"summaries", "invoices"}, "a write fed by another table"},

		{"INSERT INTO invoices (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET total = ?",
			[]string{"invoices"}, "an upsert names one table: what follows the second UPDATE is SET, and reading it as a table invents a join"},

		{"SELECT id FROM public.invoices WHERE public.invoices.tenant_id = ?", []string{"invoices"}, "a schema qualifier is not a second table"},
		{"SELECT count(*) FROM (SELECT id FROM invoices) t", []string{"invoices"}, "a derived table names nothing of its own"},
		{"SELECT id FROM invoices ORDER BY created_at LIMIT ?", []string{"invoices"}, "a clause keyword is not a table"},
	} {
		got := tablesNamed(c.sql)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("tablesNamed(%q) = %v, want %v: %s", c.sql, got, c.want, c.why)
		}
	}
}
