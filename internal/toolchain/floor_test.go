package toolchain_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/toolchain"
)

// TestAnOlderCLIIsRefusedWithTheCommandThatFixesIt.
//
// The failure this replaces is the worst kind: correct, detailed, and about the
// wrong thing. A project written with a current CLI, opened with an older one,
// is refused view by view -- sixty messages naming lines that are fine -- and
// nothing in that output says the CLI is what is old.
func TestAnOlderCLIIsRefusedWithTheCommandThatFixesIt(t *testing.T) {
	err := toolchain.CheckVersion("0.16.0", "v0.17.0")
	if err == nil {
		t.Fatal("an older CLI was accepted")
	}

	for _, want := range []string{"0.16.0", "v0.17.0", "brew upgrade", "go install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not mention %q:\n%s", want, err)
		}
	}
}

// TestTheSameOrNewerIsAccepted. A floor is a floor, not a pin: a project asking
// for 0.17.0 works on 0.22.0, and refusing that would make every release break
// every project.
func TestTheSameOrNewerIsAccepted(t *testing.T) {
	for _, running := range []string{"0.17.0", "v0.17.0", "0.17.1", "0.22.0", "1.0.0"} {
		if err := toolchain.CheckVersion(running, "v0.17.0"); err != nil {
			t.Errorf("aru %s was refused against a floor of v0.17.0: %v", running, err)
		}
	}
}

// TestWhatIsNotRefused, and each of these would break somebody for no reason.
func TestWhatIsNotRefused(t *testing.T) {
	for _, c := range []struct {
		what           string
		running, floor string
	}{
		// Every project generated before this existed.
		{"a project with no floor", "0.16.0", ""},
		// Somebody running their own build knows which build it is.
		{"a CLI built from source", "dev", "v0.99.0"},
		// A floor nobody can parse is a floor nobody wrote on purpose, and
		// refusing on it would break a project over a typo.
		{"a floor that is not a version", "0.16.0", "latest"},
		{"a floor with two parts", "0.16.0", "v1.2"},
	} {
		if err := toolchain.CheckVersion(c.running, c.floor); err != nil {
			t.Errorf("%s was refused: %v", c.what, err)
		}
	}
}

// TestTheComparisonIsNumeric: 0.9.0 is older than 0.10.0, and a string compare
// says the opposite.
func TestTheComparisonIsNumeric(t *testing.T) {
	if err := toolchain.CheckVersion("0.9.0", "v0.10.0"); err == nil {
		t.Error("0.9.0 was accepted against a floor of 0.10.0: the comparison is lexical")
	}
	if err := toolchain.CheckVersion("0.10.0", "v0.9.0"); err != nil {
		t.Errorf("0.10.0 was refused against a floor of 0.9.0: %v", err)
	}
}
