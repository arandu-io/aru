package doctor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// TestTheCatalogueIsTheSourceReadBack is the claim the whole arrangement makes:
// the list of actions cannot disagree with the code, because it is the code.
//
// The gaps fixture is read because it holds both shapes an action is written
// in -- two constants in a policy and two conversions at the places they are
// used -- and one that must not reach a catalogue at all.
func TestTheCatalogueIsTheSourceReadBack(t *testing.T) {
	actions, err := doctor.Actions(fixture(t, "gaps"))
	if err != nil {
		t.Fatalf("Actions: %v", err)
	}

	byValue := map[string]doctor.Action{}
	for _, a := range actions {
		if seen, twice := byValue[a.Value]; twice {
			t.Errorf("%s is listed twice: %s:%d and %s:%d",
				a.Value, seen.File, seen.Line, a.File, a.Line)
		}
		byValue[a.Value] = a
	}

	// The declared form, which is what a module offers a screen: the identifier
	// a permission is stored under, separate from the string Grant.Check
	// compares.
	declared, found := byValue["report.delete"]
	if !found {
		t.Fatalf("report.delete is declared and was not read; got %v", keysOf(byValue))
	}
	if declared.Const != "policies.ActionDeleteReport" {
		t.Errorf("the constant is %q, want policies.ActionDeleteReport", declared.Const)
	}
	if declared.File != "app/Policies/ReportPolicy.go" || declared.Line == 0 {
		t.Errorf("report.delete points at %s:%d, and an entry nobody can open is a list somebody typed",
			declared.File, declared.Line)
	}

	// The undeclared form. It is not named and it is fixed in the source, so
	// leaving it out would produce a catalogue missing an action the code
	// enforces -- which is the divergence the catalogue exists to prevent.
	for _, value := range []string{"report.export", "report.restore"} {
		converted, found := byValue[value]
		if !found {
			t.Errorf("%s is written at the place it is used and was not read", value)
			continue
		}
		if converted.Const != "" {
			t.Errorf("%s reports the constant %q and no constant declares it", value, converted.Const)
		}
	}
}

// TestTheCatalogueReadsATreeThatDoesNotCompile is what separates reading the
// source from asking a running process.
//
// A project is checked and administered when it is broken at least as often as
// when it is whole, and a catalogue that needed the binary would answer nothing
// exactly then.
func TestTheCatalogueReadsATreeThatDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	source := `package policies

import "github.com/arandu-io/framework/security"

// InvoiceApprove is signing one off.
const InvoiceApprove security.Action = "invoice.approve"

func Broken() {
`
	if err := os.WriteFile(filepath.Join(dir, "policy.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second file, whole, so the answer comes from the tree rather than from
	// the one file that happened to parse.
	whole := `package policies

import "github.com/arandu-io/framework/security"

// InvoiceVoid is cancelling one.
const InvoiceVoid security.Action = "invoice.void"
`
	if err := os.WriteFile(filepath.Join(dir, "void.go"), []byte(whole), 0o644); err != nil {
		t.Fatal(err)
	}

	actions, err := doctor.Actions(dir)
	if err != nil {
		t.Fatalf("Actions: %v", err)
	}
	if len(actions) != 1 || actions[0].Value != "invoice.void" {
		t.Fatalf("read %v, want the one action in the file that parses", actions)
	}
	if actions[0].Doc != "cancelling one." {
		t.Errorf("the comment is %q, and a screen shows it beside the identifier", actions[0].Doc)
	}
}

// TestAnActionOfAnotherTypeIsNotRead guards the one way this list can be wrong
// in the direction that matters.
//
// Action is a common word. A catalogue that offered a permission built out of
// somebody's own unrelated type would be offering a permission no policy reads,
// and a screen granting it would grant nothing.
func TestAnActionOfAnotherTypeIsNotRead(t *testing.T) {
	dir := t.TempDir()
	source := `package audit

// Action is this package's own, and has nothing to do with authorization.
type Action string

// Recorded is what the audit log writes.
const Recorded Action = "audit.recorded"
`
	if err := os.WriteFile(filepath.Join(dir, "audit.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	actions, err := doctor.Actions(dir)
	if err != nil {
		t.Fatalf("Actions: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("a type called Action that is not the authorization one was read: %v", actions)
	}
}

// TestAnActionBuiltAtRunTimeIsReported is the other half of the same guarantee:
// the catalogue can only be complete if nothing makes an action it cannot see.
func TestAnActionBuiltAtRunTimeIsReported(t *testing.T) {
	findings, err := doctor.Run(fixture(t, "violations"), doctor.Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var reported int
	for _, f := range findings {
		if f.Rule != "action-not-a-constant" {
			continue
		}
		reported++
		if f.Severity != doctor.Error {
			t.Errorf("%s is a warning, and what it reports is refused at run time on a path nobody can trace", f.File)
		}
	}
	if reported == 0 {
		t.Error("the fixture builds an action out of a parameter, a formatter and a map, and none of it was reported")
	}
}

func keysOf(m map[string]doctor.Action) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
