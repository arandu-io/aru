package unit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/aru/internal/testlayout"
	"github.com/arandu-io/aru/tests"
)

// The layout this repository's suite is required to have, checked by command
// rather than by review.
//
// The checks themselves live in internal/testlayout, because they answer about
// two trees: this repository, below, and the application `aru` generates, which
// `aru doctor` asks the same four questions of. Writing them twice is how the
// two would come to disagree, and the disagreement would be silent -- both
// copies pass on a tree that satisfies them.
//
// What stays here is what only this repository can say: that its own suite is
// not optional, and the planting below.

// TestTheLayoutIsWhatItClaims runs the four checks over this repository.
//
// Examined is asserted, not just Problems. Every statement these checks make is
// of the form "every X is Y", and every one of them is true of no X at all: a
// check handed none of the kind of file it is about reports nothing wrong, and
// in a log that is the same green as a tree that passes. This repository has
// tests, has files that ship, and has both under a module -- so a zero here is
// the walk looking in the wrong place, and nothing else.
func TestTheLayoutIsWhatItClaims(t *testing.T) {
	root := tests.Root(t)

	for _, c := range testlayout.Checks() {
		result, err := c.Run(root)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if result.Examined == 0 {
			t.Errorf("%s examined no file at all, so it had nothing to be true of", c.Name)
		}
		for _, rel := range result.Unreadable {
			t.Errorf("%s could not parse %s, so what it says about that file is nothing", c.Name, rel)
		}
		for _, p := range result.Problems {
			t.Errorf("%s: %s\n    %s", c.Name, p, p.Why)
		}
	}
}

// TestTheGuardCatchesWhatItIsFor plants each mistake and watches it be caught.
//
// A guard is accepted because it passed, and a guard that passes on everything
// passes too. Every check was written against a tree that already satisfied it,
// which is how six defects shipped in a version of this guard: a name pattern
// that missed view_Test.go, a path anchor that let app/Services/tests/ through,
// a scaffolding check asking a question whose answer was always yes, a
// scaffolding check that could not match the one directory the rule is named
// after, a walk that reported success when it read nothing, and a check that
// reported success when the walk read plenty and none of it was its business.
//
// # Naming a probe
//
// The seventh defect was in this method rather than in what it measures, and it
// is the one nobody rediscovers cheaply.
//
// Two probes were called Handler_Test.go and handler_test.go. The default
// filesystem on macOS is case-insensitive, so those are one file: the second
// write replaced the contents of the first, under the first one's name, and the
// probe for one check silently ceased to exist. That check then reported clean
// -- correctly, on a tree that no longer held its mistake -- and read as a pass.
//
// So: **no two probes may differ only by case.** A probe that disappears makes a
// check look right for the wrong reason, which is the exact failure this whole
// file exists to refuse, arriving through the thing meant to refuse it.
func TestTheGuardCatchesWhatItIsFor(t *testing.T) {
	root := plant(t)

	for _, c := range []struct {
		name  string
		run   func(root string) (testlayout.Result, error)
		wants []string
	}{
		{"a test go test does not run", testlayout.InvisibleTests, []string{
			"app/ButtonTest.go",   // the name, with a letter before Test
			"app/view_Test.go",    // the name, with an underscore before Test
			"app/TestBroker.go",   // the contents, under a name no pattern reaches
			"app/benchmarks.go",   // a Benchmark counts, and its name says nothing
			"app/fuzzcorpus.go",   // so does a Fuzz
			"app/Handler_Test.go", // both reasons at once
		}},
		{"a test outside tests/ without the _internal suffix", testlayout.MisplacedTests, []string{
			"app/dispatch_test.go",             // beside the code, no suffix
			"app/Services/tests/order_test.go", // a directory called tests that is not the suite
		}},
		{"a capitalised package clause", testlayout.CapitalisedPackages, []string{
			"app/Cap.go",
		}},
		{"test scaffolding reached by something that ships", testlayout.ScaffoldingInProduction, []string{
			"app/wire.go",   // the base package
			"app/helped.go", // tests/Helpers, which is what the rule is named after
			"cmd/serve.go",  // an aliased import is the same import
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := c.run(root)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			caught := map[string]bool{}
			for _, p := range result.Problems {
				caught[p.Path] = true
			}
			for _, want := range c.wants {
				if !caught[want] {
					t.Errorf("%s went unreported, and the mistake in it is the one this check exists for", want)
				}
			}
			for path := range caught {
				if !contains(c.wants, path) {
					t.Errorf("%s was reported and is correct: a check that invents findings is one people turn off", path)
				}
			}
		})
	}
}

// TestEveryProbeSurvivesBeingWritten is the seventh defect, refused by command.
//
// The comment above tells whoever adds a probe not to name two of them the same
// apart from case. This checks it, because the failure is silent in exactly the
// way that makes a written warning insufficient: the tree ends up holding fewer
// files than were written, every check still runs, and the one whose probe
// vanished reports clean.
//
// It compares against what the directory actually holds, entry by entry, and
// that is the whole of it. Asking os.Stat whether each probe is there answers
// yes on the filesystem where the problem exists -- a lookup of
// handler_test.go resolves to Handler_Test.go and reports a file that was never
// written. The names have to be read back, not asked about.
func TestEveryProbeSurvivesBeingWritten(t *testing.T) {
	root := plant(t)

	onDisk := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		onDisk[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range planted {
		if !onDisk[want] {
			t.Errorf("%s was written and the directory does not hold it: another probe differs from it only by "+
				"case, and where the filesystem ignores case the two are one file -- the second write replaced the "+
				"first under the first one's name. The check this probe belongs to now reports clean on a tree "+
				"with nothing in it to find", want)
		}
	}
	for got := range onDisk {
		if !contains(planted, got) {
			t.Errorf("%s is on disk and not in planted: the list is what makes a vanished probe visible, "+
				"and it only works while it says what plant writes", got)
		}
	}
}

// TestTheChecksSayWhenTheyExaminedNothing is the bug that outlived all the
// others: every check answered "nothing wrong" when it had read nothing at all.
//
// It came back a second time in a narrower form, which is why the middle case
// exists. Each check is about a different part of the tree, and a walk that
// reads nine hundred files can still hand one of them none of the kind it is
// about -- "every _test.go file is under tests/" is true of a tree with no test
// in it.
//
// Examined is reported rather than turned into an error, because the two callers
// want opposite things from a zero: this repository's suite is not optional, and
// an application generated this morning has not been given a test yet.
func TestTheChecksSayWhenTheyExaminedNothing(t *testing.T) {
	empty := t.TempDir()
	write(t, empty, "go.mod", "module example.test/empty\n")
	for _, c := range testlayout.Checks() {
		result, err := c.Run(empty)
		if err != nil {
			t.Errorf("%s on a tree with no Go in it: %v", c.Name, err)
			continue
		}
		if result.Examined != 0 {
			t.Errorf("%s examined %d files in a directory holding none", c.Name, result.Examined)
		}
	}

	// A tree with Go in it, and none of the kind a particular check examines.
	// Named one by one, because "some check saw nothing" would pass on the
	// wrong one having seen nothing.
	for _, c := range []struct {
		holds string
		body  string
		blind []string
	}{
		{"app/service.go", "package app\n", []string{
			"a test outside tests/ without the _internal suffix", // no test file to place
		}},
		{"tests/Unit/only_test.go", "package unit_test\n", []string{
			"a test go test does not run",                      // nothing that ships
			"test scaffolding reached by something that ships", // the same
		}},
	} {
		lopsided := t.TempDir()
		write(t, lopsided, "go.mod", "module example.test/lopsided\n")
		write(t, lopsided, c.holds, c.body)

		for _, check := range testlayout.Checks() {
			result, err := check.Run(lopsided)
			if err != nil {
				t.Errorf("%s: %v", check.Name, err)
				continue
			}
			if blind := contains(c.blind, check.Name); blind != (result.Examined == 0) {
				t.Errorf("a tree holding only %s: %q examined %d, and it should have examined %s",
					c.holds, check.Name, result.Examined, map[bool]string{true: "nothing", false: "something"}[blind])
			}
		}
	}
}

// TestAFileThatDoesNotParseIsCarriedNotSkipped.
//
// A check that skips a file it could not read has concluded nothing about it
// and says everything is fine. The file is reported instead, and left to the
// caller: this suite treats it as a failure, and a report whose first rule
// already says "this file does not parse" does not want it said twice.
//
// The check that reasons about names and paths is expected to say nothing about
// it, because a name is as readable in a file that does not compile as in one
// that does.
func TestAFileThatDoesNotParseIsCarriedNotSkipped(t *testing.T) {
	broken := t.TempDir()
	write(t, broken, "go.mod", "module example.test/broken\n")
	write(t, broken, "app/half.go", "package app\n\nfunc Open( {\n")
	write(t, broken, "tests/Unit/whole_test.go", "package unit_test\n")

	for _, c := range testlayout.Checks() {
		result, err := c.Run(broken)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if c.Reads != (len(result.Unreadable) == 1) {
			t.Errorf("%s reads contents = %t, and reported %v as unreadable", c.Name, c.Reads, result.Unreadable)
		}
	}
}

// planted is every file plant writes, so one that does not survive the writing
// is caught rather than assumed.
var planted = []string{
	"go.mod",
	"app/ButtonTest.go",
	"app/view_Test.go",
	"app/Handler_Test.go",
	"app/TestBroker.go",
	"app/benchmarks.go",
	"app/fuzzcorpus.go",
	"app/templates.go",
	"app/testing.go",
	"app/dispatch_test.go",
	"app/Services/tests/order_test.go",
	"tests/Unit/ok_test.go",
	"app/parse_internal_test.go",
	"app/sub/go.mod",
	"app/sub/tests/Unit/sub_test.go",
	"app/Cap.go",
	"app/wire.go",
	"app/helped.go",
	"cmd/serve.go",
	"tests/Helpers/helpers.go",
	"app/near.go",
}

// plant builds a tree holding one of each mistake, and the correct shapes that
// each check must stay quiet about.
//
// Adding a probe means adding its path to planted above. See the note on naming
// in TestTheGuardCatchesWhatItIsFor: two probes differing only by case are one
// file on the filesystem most of this is written on.
func plant(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write(t, root, "go.mod", "module example.test/probe\n\ngo 1.26\n")

	// Check 1, six ways. The three named files are what a pattern over names
	// has to reach; the three ordinary ones are what it cannot.
	write(t, root, "app/ButtonTest.go", "package app\n\nimport \"testing\"\n\nfunc TestButton(t *testing.T) {}\n")
	write(t, root, "app/view_Test.go", "package app\n\nimport \"testing\"\n\nfunc TestView(t *testing.T) {}\n")
	write(t, root, "app/Handler_Test.go", "package app\n")
	write(t, root, "app/TestBroker.go", "package app\n\nimport \"testing\"\n\nfunc TestBroker(t *testing.T) {}\n")
	write(t, root, "app/benchmarks.go", "package app\n\nimport \"testing\"\n\nfunc BenchmarkPush(b *testing.B) {}\n")
	write(t, root, "app/fuzzcorpus.go", "package app\n\nimport \"testing\"\n\nfunc FuzzParse(f *testing.F) {}\n")

	// And what check 1 must not report: a test function inside a string
	// literal, which is how the generator carries the file it emits.
	write(t, root, "app/templates.go", "package app\n\nconst tmpl = `package unit_test\n\nfunc TestGenerated(t *testing.T) {}\n`\n")
	// A name one rune away from a test on both counts.
	write(t, root, "app/testing.go", "package app\n\nimport \"testing\"\n\nfunc Testing(t *testing.T) {}\n\nfunc TestHelper(t *testing.T, name string) {}\n")

	// Check 2. The first is the ordinary mistake; the second is the one an
	// anchor on any path element lets through.
	//
	// Not handler_test.go, which is the name that first suggests itself: it
	// differs from Handler_Test.go above only by case.
	write(t, root, "app/dispatch_test.go", "package app_test\n")
	write(t, root, "app/Services/tests/order_test.go", "package tests_test\n")
	// And what it must not report.
	write(t, root, "tests/Unit/ok_test.go", "package unit_test\n")
	write(t, root, "app/parse_internal_test.go", "package app\n")
	// A nested module keeps its own tests/, measured from its own go.mod.
	write(t, root, "app/sub/go.mod", "module example.test/probe/app/sub\n")
	write(t, root, "app/sub/tests/Unit/sub_test.go", "package unit_test\n")

	// Check 3.
	write(t, root, "app/Cap.go", "package App\n")

	// Check 4, three ways in. The Helpers one is the case the rule is named
	// after and the only one reachable in practice: tests/Helpers holds
	// ordinary packages, so importing one compiles.
	write(t, root, "app/wire.go", "package app\n\nimport _ \"example.test/probe/tests\"\n")
	write(t, root, "app/helped.go", "package app\n\nimport _ \"example.test/probe/tests/Helpers\"\n")
	write(t, root, "cmd/serve.go", "package main\n\nimport scaffold \"example.test/probe/tests/Helpers\"\n\nvar _ = scaffold.X\n")
	// And what it must not report: the suite reaching for its own helpers,
	// which is what a suite is for, from a file that is not a _test.go one.
	write(t, root, "tests/Helpers/helpers.go", "package helpers\n\nimport _ \"example.test/probe/tests\"\n")
	// A module whose path merely starts the same way is somebody else's.
	write(t, root, "app/near.go", "package app\n\nimport _ \"example.test/probe/testsuite\"\n")

	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
