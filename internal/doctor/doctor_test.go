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
	if !strings.Contains(caught.File, ".templ") {
		t.Errorf("the finding does not point at the template: %s", caught.File)
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
			if !strings.Contains(f.File, "policy") {
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
