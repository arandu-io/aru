package doctor

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestEveryRuleFiresOnAFixture walks the rule set and demands that each one
// produce at least one finding across the fixtures.
//
// It exists because of a failure that happened six times in one day, always the
// same shape: a document states that something is checked, and nobody checks
// that the check exists. ADR 0024 asserted that sql-built-with-sprintf had been
// widened to concatenation -- in the paragraph arguing the project does not need
// sqlc -- and it had not. A mutation run found two other rules that could be
// deleted whole with the suite still green.
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

	// E a direcao inversa, que e a que o harness de mutacao achou: uma regra
	// APAGADA da slice nao aparece no laco acima, entao some sem deixar rastro.
	// Duas ja podiam ser deletadas inteiras com o CI verde.
	//
	// O mapa emits e a lista do que tem que existir. Tirar uma regra da slice sem
	// tirar do mapa reprova aqui, e tirar das duas e uma decisao explicita, que e
	// como uma remocao de regra de seguranca deve parecer num diff.
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
		"unreadableFiles":                     {"file-does-not-parse"},
		"repositoryNeedsPolicy":               {"repository-without-policy"},
		"repositoryMethodNeedsGrant":          {"grant-not-checked", "grant-not-received", "grant-check-discarded"},
		"policyMustBeOpened":                  {"policy-never-opened"},
		"controllerMustNotReachData":          {"handler-reaches-data"},
		"controllerMustNotReachTheRepository": {"controller-reaches-repository"},
		"tenantMustComeFromTheGrant":          {"tenant-from-request", "tenant-from-header"},
		"systemGrantIsAudited":                {"system-grant-without-tenant", "system-grant-outside-scope"},
		"noBuiltSQL":                          {"sql-built-with-sprintf", "sql-built-by-concatenation"},
		"sensitiveFieldNeedsRedaction":        {"sensitive-field-not-redacted"},
		"sessionMustRotateOnLogin":            {"session-not-rotated"},
		"viewDataMustBeAStruct":               {"view-data-is-a-map"},
		"viewMustExist":                       {"view-does-not-exist"},
		"declaredPermissionsMatchTheCode":     {"permission-not-declared", "permission-not-used"},
		"alpineHoldsClientStateOnly":          {"alpine-reaches-the-server"},
		"tenantMustScopeTheSQL":               {"sql-without-tenant-scope"},
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
