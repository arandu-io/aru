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
		findings, err := Run(dir)
		if err != nil {
			continue // a fixture that does not load is the other tests' problem
		}
		for _, f := range findings {
			fired[f.Rule] = true
		}
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
