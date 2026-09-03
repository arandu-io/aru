package main

import (
	"strings"
	"testing"
)

// insideAProject writes the three files projectRoot looks for and moves into
// them.
//
// The font commands find the project before they read a single argument, so a
// test of the parser that skipped this would be measuring the "not an Arandu
// project" message instead.
func insideAProject(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root+"/go.mod", "module example.test/parse\n")
	writeFile(t, root+"/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, root+"/arandu.toml", "name = \"parse\"\n")
	chdir(t, root)
}

// TestAValueAttachesEitherWay: --as=body and --as body are one argument written
// two ways, and both are read.
//
// The value is observed through the message that refuses an invalid role, which
// is the last thing font:add checks before it reaches the network. That message
// quotes the value it read, so a spelling that never became a value cannot
// produce it -- and the flag written last proves the words around it are still
// collected as the family.
func TestAValueAttachesEitherWay(t *testing.T) {
	insideAProject(t)

	for _, args := range [][]string{
		{"font:add", "Inter", "--as=bogus"},
		{"font:add", "Inter", "--as", "bogus"},
		{"font:add", "--as=bogus", "Inter"},
		{"font:add", "Inter", "--as=bogus", "--weight=400..700"},
	} {
		code, _, stderr := exercise(t, args...)
		if code == 0 {
			t.Errorf("aru %s exited 0 with an invalid role", strings.Join(args, " "))
		}
		if !strings.Contains(stderr, `"bogus"`) {
			t.Errorf("aru %s: --as never became a value: %q", strings.Join(args, " "), stderr)
		}
	}
}

// TestFontSearchReadsBothSpellings is the same claim on the other command that
// reads its arguments by hand.
//
// The category is checked before the catalogue is fetched, so this asks nothing
// of the network.
func TestFontSearchReadsBothSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"font:search", "--category=nonsense"},
		{"font:search", "--category", "nonsense"},
		{"font:search", "--category=nonsense", "grotesk"},
	} {
		code, _, stderr := exercise(t, args...)
		if code == 0 {
			t.Errorf("aru %s exited 0 with an invalid category", strings.Join(args, " "))
		}
		if !strings.Contains(stderr, "is not a category") {
			t.Errorf("aru %s: --category never became a value: %q", strings.Join(args, " "), stderr)
		}
	}
}

// TestASwitchRefusesAnAttachedValue guards the one place reading the name alone
// would answer the opposite of what was typed.
//
// --variable=false says to leave the variable families out. Read as the name
// only, it turns the filter on.
func TestASwitchRefusesAnAttachedValue(t *testing.T) {
	code, _, stderr := exercise(t, "font:search", "--variable=false")

	if code == 0 {
		t.Error("--variable=false was accepted, and a switch cannot honour a value")
	}
	if !strings.Contains(stderr, "takes no value") {
		t.Errorf("the refusal does not say what is wrong: %q", stderr)
	}
	if !strings.Contains(stderr, "--variable") {
		t.Errorf("the refusal does not name the flag: %q", stderr)
	}
}

// TestAnUnknownFlagSaysHowAValueAttaches: the message a person meets when they
// guess wrong has to carry the answer.
//
// "unknown flag" and nothing else is what the reader gets in the case they are
// most often in -- a flag that does exist, written with its value attached --
// and it sends them looking for a flag that is there.
func TestAnUnknownFlagSaysHowAValueAttaches(t *testing.T) {
	insideAProject(t)

	code, _, stderr := exercise(t, "font:add", "Inter", "--nope=1")
	if code == 0 {
		t.Error("an unknown flag exited 0")
	}
	if !strings.Contains(stderr, "--nope") {
		t.Errorf("the refusal does not name the argument: %q", stderr)
	}
	if !strings.Contains(stderr, "--flag=value") || !strings.Contains(stderr, "--flag value") {
		t.Errorf("the refusal does not say how a value attaches: %q", stderr)
	}
}
