package doctor

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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
		"controllerMustNotReachData":              {"handler-reaches-data"},
		"controllerMustNotReachTheRepository":     {"controller-reaches-repository"},
		"tenantMustComeFromTheGrant":              {"tenant-from-request", "tenant-from-header"},
		"systemGrantIsAudited":                    {"system-grant-without-tenant", "system-grant-outside-scope"},
		"noBuiltSQL":                              {"sql-built-with-sprintf", "sql-built-by-concatenation"},
		"sensitiveFieldNeedsRedaction":            {"sensitive-field-not-redacted"},
		"sessionMustRotateOnLogin":                {"session-not-rotated"},
		"viewDataMustBeAStruct":                   {"view-data-is-a-map"},
		"viewMustExist":                           {"view-does-not-exist"},
		"declaredPermissionsMatchTheCode":         {"permission-not-declared", "permission-not-used"},
		"alpineHoldsClientStateOnly":              {"alpine-reaches-the-server"},
		"tenantMustScopeTheSQL":                   {"sql-without-tenant-scope"},
		"theOutboxTableTravelsWithWhatWritesToIt": {"outbox-not-registered"},
		"resourceNotReauthorized":                 {"resource-not-reauthorized"},
		"rawOutputIsAComponent":                   {"raw-output-is-not-a-component"},
		"theProfileIsDeclared":                    {"profile-not-declared"},
		"queriesReachOneAggregate":                {"join-across-aggregates"},
		"transactionsStayInsideOneAggregate":      {"transaction-across-aggregates"},
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestEveryGrantConstructorIsGuarded compares grantConstructors against what the
// framework actually exports.
//
// The list is written by hand because the doctor reads one package at a time and
// does not resolve types across modules -- it cannot ask "what returns a
// security.Grant" the way a compiler can. A hand-written list is exactly what
// went wrong: the rules matched `security.SystemGrant` and `jobs.GrantFor`
// wrapped it, so a Grant for an action and a tenant nobody authorized produced
// no finding at all.
//
// So this reads the framework's source next door and fails when it exports a
// Grant constructor the list does not name. security.Authorize is excluded on
// purpose: it is the mandatory path, and a Grant that came from it was
// authorized by construction.
//
// If the framework is not on disk the test skips rather than passing quietly --
// a guard that silently does nothing is the thing it exists to prevent.
func TestEveryGrantConstructorIsGuarded(t *testing.T) {
	root := filepath.Join("..", "..", "..", "framework")
	if _, err := os.Stat(root); err != nil {
		t.Skip("the framework is not checked out next to this repository")
	}

	// `func Name(...) [security.]Grant` and `... (Grant, error)`, exported only.
	decl := regexp.MustCompile(`^func ([A-Z]\w*)(\[[^]]*\])?\([^)]*\)\s*\(?(security\.)?Grant\b`)

	found := map[string]string{} // symbol -> package
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
		pkg := filepath.Base(filepath.Dir(path))
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
	if len(found) == 0 {
		t.Fatal("no Grant constructor found in the framework at all: this test stopped testing anything")
	}

	guarded := map[string]bool{}
	for _, c := range grantConstructors {
		guarded[c] = true
	}
	// The mandatory path. A Grant it returns was authorized by a Policy.
	guarded["security.Authorize"] = true

	for symbol, path := range found {
		if !guarded[symbol] {
			t.Errorf("%s (%s) hands back a security.Grant and no rule watches it: add it to grantConstructors, "+
				"and give tenantArg the shape it spells the tenant in", symbol, path)
		}
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
