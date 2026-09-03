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

// TestEveryCommandAnswersHelp walks the dispatch table, and it is the guard
// against the next command being added without it.
//
// --help is the reflex, and a command that answers it with an error is one
// whose explanation can only be found by getting the command wrong. Asserting
// it of every entry rather than of a list is what makes the next entry pay.
func TestEveryCommandAnswersHelp(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, c := range commands {
		for _, spelling := range []string{"--help", "-h"} {
			code, stdout, stderr := exercise(t, c.name, spelling)
			if code != 0 {
				t.Errorf("aru %s %s exited %d: %s", c.name, spelling, code, stderr)
				continue
			}
			if !strings.Contains(stdout, c.usage) {
				t.Errorf("aru %s %s does not print its usage line: %q", c.name, spelling, stdout)
			}
			if !strings.Contains(stdout, c.desc) {
				t.Errorf("aru %s %s does not print what it does: %q", c.name, spelling, stdout)
			}
		}
	}
}

// TestFontAddExplainsTheTwoRolesUnderHelp: the explanation that was only
// reachable by getting the command wrong is reachable by asking.
//
// Both halves are asserted because both are what the command refuses with, and
// the point of --help printing them is that there is one copy of each.
func TestFontAddExplainsTheTwoRolesUnderHelp(t *testing.T) {
	t.Chdir(t.TempDir())

	_, stdout, _ := exercise(t, "font:add", "--help")
	for _, want := range []string{
		"display is headings and the masthead",
		"aru font:search grotesk",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("aru font:add --help does not carry %q: %q", want, stdout)
		}
	}
}

// TestTheApplicationsFlagsAreNotAnswered: `aru serve -- --help` asks the
// application, and the scan for --help has to stop at the bare -- to leave it
// alone.
func TestTheApplicationsFlagsAreNotAnswered(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := exercise(t, "serve", "--", "--help")
	if code == 0 {
		t.Error("serve ran outside a project")
	}
	if strings.Contains(stdout, "usage: aru serve") {
		t.Errorf("aru answered a flag addressed to the application: %q", stdout)
	}
	if !strings.Contains(stderr, "arandu.toml") {
		t.Errorf("the command did not reach the project check: %q", stderr)
	}
}

// TestAPrefixAnswersWithItsFamily: `aru font` names a family and no command.
//
// The whole table is not the answer. It is fifty-four lines, the five being
// reached for are somewhere in it, and inside a project the name went to the
// application's binary instead -- which lists its own commands and none of them
// is a font command.
func TestAPrefixAnswersWithItsFamily(t *testing.T) {
	t.Chdir(t.TempDir())

	code, _, stderr := exercise(t, "font")
	if code == 0 {
		t.Error("a family name with no command exited 0")
	}
	for _, want := range []string{"font:add", "font:search", "font:remove"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the answer does not offer %s: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "key:generate") {
		t.Error("the whole table was printed, which is what buries the commands being reached for")
	}
}

// TestTheRefusalRepeatsTheDispatchTable: a command that refuses for want of its
// name says the line --help says.
//
// Each of these writes its own copy, and it has to stay a copy: a command
// function cannot read the table it is listed in, because the table is a
// package variable initialised from the function and Go refuses the cycle. So
// nothing but this test holds the two together, and eight of them had already
// drifted before anything printed both -- a flag in one and not the other, an
// argument called <module> in one and <entity> in the other.
func TestTheRefusalRepeatsTheDispatchTable(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, name := range []string{
		"new", "generate",
		"make:module", "make:model", "make:migration", "make:controller",
		"make:middleware", "make:request", "make:factory", "make:seeder",
		"make:job", "make:mail", "make:command", "make:listener", "make:event",
		"make:enum", "make:policy", "make:test",
	} {
		c, found := lookup(name)
		if !found {
			t.Errorf("%s is not in the dispatch table", name)
			continue
		}
		code, _, stderr := exercise(t, name)
		if code == 0 {
			t.Errorf("%s exited 0 with no name", name)
		}
		if !strings.Contains(stderr, c.usage) {
			t.Errorf("%s refuses with a usage line of its own:\n  table: %q\n  said:  %q", name, c.usage, stderr)
		}
	}
}
