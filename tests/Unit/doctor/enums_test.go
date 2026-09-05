package doctor_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// TestAListBesideATypeIsReported covers the defect and the drift separately,
// because they are not the same failure.
//
// A case the type cannot produce fails the rule for every value submitted, so
// it is broken now. A list that agrees is the copy that will drift, and it
// works today -- a project red on the day it was generated is a project whose
// developers switch the tool off.
func TestAListBesideATypeIsReported(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	bySeverity := map[doctor.Severity][]doctor.Finding{}
	for _, f := range findings {
		if f.Rule == "enum-rule-not-derived" {
			bySeverity[f.Severity] = append(bySeverity[f.Severity], f)
		}
	}

	broken := bySeverity[doctor.Error]
	if len(broken) == 0 {
		t.Fatal("the fixture lists a case the type does not declare, and nothing reported it")
	}
	if !strings.Contains(broken[0].Message, "voided") {
		t.Errorf("the finding does not name the case that cannot be produced: %q", broken[0].Message)
	}
	if !strings.Contains(broken[0].Why, "app/Enums/ChargeStatus.go") {
		t.Errorf("the finding does not point at the type that already declares the set: %q", broken[0].Why)
	}

	if len(bySeverity[doctor.Warning]) == 0 {
		t.Error("a list that agrees with the type today is the one that drifts tomorrow, and nothing reported it")
	}
}

// TestAnIntegerBackedSetIsNotJudged is the limitation, held in place.
//
// A list beside an integer-backed enum is written in shown spellings and the
// column holds numbers. Turning one into the other needs the switch inside the
// type, so nobody can say from the source whether the list agrees -- and a rule
// that reported anyway would be inventing the answer.
//
// The fixture also holds `enum` with no list, which is the shape this rule
// exists to ask for, and a list on a field with no type behind it, where the
// list is the only set there is.
func TestAnIntegerBackedSetIsNotJudged(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "gaps"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, f := range findings {
		if f.Rule == "enum-rule-not-derived" {
			t.Errorf("a case nobody can compare was reported anyway: %s", f)
		}
	}
}

// TestTheGeneratedRequestIsNotReported is the other direction of the same
// claim: the tool must not report the shape its own generator emits.
func TestTheGeneratedRequestIsNotReported(t *testing.T) {
	for _, name := range []string{"clean", "orm"} {
		findings, err := doctor.Run(fixture(t, name), doctor.Conventional)
		if err != nil {
			t.Fatalf("Run(%s): %v", name, err)
		}
		for _, f := range findings {
			if f.Rule == "enum-rule-not-derived" {
				t.Errorf("%s is the shape the generator emits and it was reported: %s", name, f)
			}
		}
	}
}
