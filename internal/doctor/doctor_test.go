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
