package pack

import (
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are three directories of Go that is never built: the go tool
// ignores testdata, which is what lets one of them declare an identifier that
// is not a string. They hold no toolchain, reach no network and name no other
// module, because the fault they stand for is found by reading and the check
// has to run on a machine with nothing installed.
func runtimeFixture(name string) string {
	return filepath.Join("testdata", "runtime", name)
}

// TestARuntimeThatCanBeToldWhatTheApplicationIsPasses is the case that must not
// become expensive: every ordinary packaging run pays for this check.
//
// It also fixes that a build constraint does not hide a declaration. One of the
// three is declared in a file only Windows compiles, and the flag naming it is
// only passed for Windows -- so a check that read the files of the machine
// doing the packaging would refuse every build on every other platform.
func TestARuntimeThatCanBeToldWhatTheApplicationIsPasses(t *testing.T) {
	if err := verifyLinkedSymbols("example.test/engine/app", runtimeFixture("complete")); err != nil {
		t.Errorf("a complete runtime package was refused: %v", err)
	}
}

// TestAMissingSymbolIsRefusedByName fixes the failure that otherwise has no
// symptom at all.
//
// `go tool link -X` over a name that is not there writes nothing and says
// nothing. The APK builds, installs, opens, and its application identifier is
// empty -- which on a phone is the identity an upgrade is matched against, so
// it surfaces as a second copy installed beside the first, on somebody else's
// device.
func TestAMissingSymbolIsRefusedByName(t *testing.T) {
	err := verifyLinkedSymbols("example.test/engine/app", runtimeFixture("incomplete"))
	if err == nil {
		t.Fatal("a runtime package missing one of the three was accepted")
	}
	if !strings.Contains(err.Error(), "schemesURI") {
		t.Errorf("the refusal does not name the symbol that is missing: %v", err)
	}
	if !strings.Contains(err.Error(), "example.test/engine/app") {
		t.Errorf("the refusal does not say which package was read: %v", err)
	}
}

// TestASymbolOfTheWrongTypeIsRefusedByType covers the half of the rule that
// being present does not satisfy.
//
// The linker writes strings. A name of any other type is matched and skipped,
// with the same silence as one that was never declared, so "it is declared" is
// not the question.
func TestASymbolOfTheWrongTypeIsRefusedByType(t *testing.T) {
	err := verifyLinkedSymbols("example.test/engine/app", runtimeFixture("mistyped"))
	if err == nil {
		t.Fatal("a runtime package whose identifier is an int was accepted")
	}
	if !strings.Contains(err.Error(), "ID") || !strings.Contains(err.Error(), "int") {
		t.Errorf("the refusal does not say which symbol has which type: %v", err)
	}
}

// TestADirectoryWithNoGoIsRefused keeps the check from passing by having read
// nothing.
//
// Every statement it makes is of the form "this name is declared and is a
// string", and every one of them is false of an empty directory -- but the
// three would then be reported as three missing symbols, which reads as a
// broken runtime package rather than as a directory that was not there.
func TestADirectoryWithNoGoIsRefused(t *testing.T) {
	err := verifyLinkedSymbols("example.test/engine/app", t.TempDir())
	if err == nil {
		t.Fatal("a directory holding no Go was read as a runtime package")
	}
	if !strings.Contains(err.Error(), "no Go source") {
		t.Errorf("the refusal does not say the directory was empty: %v", err)
	}
}

// TestAConstantIsNotSomethingTheLinkerCanWriteTo is the third shape of the same
// silence.
//
// A constant is folded into every use of it before the linker exists, so -X
// over one changes nothing anywhere.
func TestAConstantIsNotSomethingTheLinkerCanWriteTo(t *testing.T) {
	declared, err := packageVariables(runtimeFixture("complete"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := declared["ID"]; !ok {
		t.Fatal("the fixture no longer declares ID, so this test reads nothing")
	}
	if declared["ID"].constant {
		t.Error("a variable was read as a constant")
	}
}
