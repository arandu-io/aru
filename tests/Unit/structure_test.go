package unit_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/arandu-io/aru/tests"
)

// The layout the suite is required to have, checked by command rather than by
// review.
//
// Four checks, in the order they matter, and the first one is the reason the
// rest exist: go test runs a file only when its name ends in _test.go, so a
// file one letter away from that pattern compiles into the package as ordinary
// code and every test inside it is skipped -- no error, no warning, a green
// build over a suite that never executed. Nothing else here can fail that
// quietly.
//
// Each check is a function over a root directory rather than a body of test
// code, so the test below can point it at a tree with the mistake planted in it
// and watch it be caught. A guard accepted because it passed is a guard nobody
// measured, and six separate defects reached a shipped version of this guard
// that way -- four of them the same defect wearing different clothes: a check
// that read nothing, or read the wrong thing, and reported success.

// problem is one thing a check found: where it is, and what breaks.
type problem struct {
	path string
	says string
}

func (p problem) String() string { return p.path + ": " + p.says }

// goFiles is every .go file under root that the go command compiles, as a slash
// path relative to root.
//
// Two things are left out, and they are the two filters the gofmt step in CI
// carries for the same reasons:
//
//	testdata/   the go command refuses to load it, so nothing in it is
//	            compiled and nothing in it can be a test that silently does
//	            not run. This repository keeps Go in there that does not parse
//	            on purpose, so the doctor has something broken to find
//	*.kyse.go   a view. It ends in .go and is not Go: the build tag keeps the
//	            compiler away from the markup below the package clause
//
// It fails when it finds nothing rather than answering with an empty list. A
// walk looking in the wrong place returns no files and every check built on it
// passes, which is the shape of green this whole file exists to refuse.
func goFiles(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, ".kyse.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no .go file under %s: the walk is looking in the wrong place, and every check built on it would pass on nothing", root)
	}
	return found, nil
}

// subjects is the files one check examines, and it fails when there are none.
//
// Each check looks at a different part of the tree -- the files that ship, the
// files that end in _test.go -- and an empty part is the failure this file was
// written twice to refuse. Every statement here is of the form "every X is Y",
// and every one of them is true of no X at all: a check whose subject set came
// back empty reports no problem, and in a CI log "true of nothing" and "true"
// are the same green.
//
// goFiles already refuses a walk that read nothing. This is the same refusal
// one level in, because a walk can find nine hundred files and still hand a
// check none of the kind it is about.
func subjects(root, of string, keep func(rel string) bool) ([]string, error) {
	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range files {
		if keep(rel) {
			out = append(out, rel)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s under %s: this check examined nothing, and a check that examined nothing has no problem to report", of, root)
	}
	return out, nil
}

// isTestFile reports whether go test would run the file rather than compile it
// into the package.
func isTestFile(rel string) bool { return strings.HasSuffix(rel, "_test.go") }

// parsed reads one file as Go.
//
// A file that does not parse is returned as an error rather than skipped. The
// compiler reports a syntax error better than this could, and that is beside
// the point: a check that concludes "no test function here" from a file it
// could not read has concluded nothing.
func parsed(root, rel string) (*ast.File, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return f, nil
}

// 1. A test go test does not run.
//
// Two ways in, and both are checked because each misses the other:
//
// The name. A file called ButtonTest.go is compiled into its package and run by
// nothing. So is view_Test.go, and it is the one a pattern gets wrong: written
// as [A-Za-z]Test\.go it catches the first and lets the second through, because
// the character before Test is an underscore. Every .go file whose name ends in
// Test.go is reported, which cannot collide with a real one -- _test.go ends in
// a lowercase t.
//
// The contents. A file called TestBroker.go, BrokerTests.go or broker.go fails
// in exactly the same way once a test function is inside it, and no pattern over
// names reaches any of them.
//
// The contents are read as declarations and not as text, which matters here:
// internal/gen/templates.go holds the source of the test file the generator
// emits, `func TestEvery...` and all, inside a string literal. A grep reports
// it, and it is the one file in the repository where a test function in a
// non-test file is exactly right.
func invisibleTests(root string) ([]problem, error) {
	files, err := subjects(root, "file that go test compiles rather than runs", func(rel string) bool {
		return !isTestFile(rel)
	})
	if err != nil {
		return nil, err
	}

	var out []problem
	for _, rel := range files {
		if strings.HasSuffix(rel, "Test.go") {
			out = append(out, problem{rel, "is named Test.go and go test runs only files ending in _test.go, " +
				"so whatever is inside it is compiled into the package and executed by nothing"})
			continue
		}

		file, err := parsed(root, rel)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isRunnableTest(fn) {
				continue
			}
			out = append(out, problem{rel, "declares " + fn.Name.Name + " and does not end in _test.go, " +
				"so go test compiles the file and runs nothing in it"})
		}
	}
	return out, nil
}

// isRunnableTest reports whether go test would run this function.
//
// Go's own rule, and not an approximation of it: one of the three prefixes,
// then either nothing or a rune that is not lower case -- Testx is an ordinary
// function and TestX is not -- with no receiver, one parameter of the matching
// testing type, and no results.
//
// The parameter is what separates a test from a helper. tests/testcase.go
// exports functions taking a testing.TB and would match on the name alone.
func isRunnableTest(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Type.Results != nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}

	var want string
	switch {
	case named(fn.Name.Name, "Test"):
		want = "T"
	case named(fn.Name.Name, "Benchmark"):
		want = "B"
	case named(fn.Name.Name, "Fuzz"):
		want = "F"
	default:
		return false
	}

	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == want
}

// named reports whether a function name carries one of go test's prefixes.
func named(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

// 2. A test that uses only the exported API and stayed beside the code.
//
// The suffix is the whole exception, and it is not a preference: a test that
// reads an unexported identifier cannot live in another package, Go decides
// that. So it stays beside the code and says so in its name, which turns "it is
// next to the code" into a claim this can check rather than a habit nobody
// notices.
//
// "tests/" means tests/ of the module the file belongs to, so the path is
// measured from the nearest go.mod at or above it. Two ways of writing that are
// wrong, and the difference between them is not cosmetic:
//
//	^tests/       anchored at the project root. A nested module keeps its own
//	              tests/, and every file in it is reported as misplaced
//	(^|/)tests/   anchored at any element. app/Services/tests/foo_test.go
//	              passes, and it is nobody's suite
func misplacedTests(root string) ([]problem, error) {
	files, err := subjects(root, "_test.go file", isTestFile)
	if err != nil {
		return nil, err
	}

	var out []problem
	for _, rel := range files {
		if strings.HasSuffix(rel, "_internal_test.go") {
			continue
		}
		inModule, err := relativeToModule(root, rel)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(inModule, "tests/") {
			continue
		}
		out = append(out, problem{rel, "is a test outside tests/: name it _internal_test.go if it reads an " +
			"unexported identifier, otherwise move it under tests/"})
	}
	return out, nil
}

// relativeToModule is rel as its own module sees it: the slash path from the
// nearest directory at or above it holding a go.mod.
//
// The search stops at root, so a file under no module at all is measured from
// root rather than from whatever happens to be above the checkout.
func relativeToModule(root, rel string) (string, error) {
	dir := filepath.Dir(filepath.Join(root, rel))
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		if dir == root || dir == filepath.Dir(dir) {
			dir = root
			break
		}
		dir = filepath.Dir(dir)
	}
	inModule, err := filepath.Rel(dir, filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(inModule), nil
}

// 3. A capitalised package clause.
//
// The directories under tests/ are capitalised and the package clauses inside
// them are not, and that is not an inconsistency to tidy up: a directory name
// is a label, an identifier is code. Every import of a capitalised package
// reads as an exported name that is not one, and go vet has nothing to say
// about it.
func capitalisedPackages(root string) ([]problem, error) {
	files, err := subjects(root, ".go file", func(string) bool { return true })
	if err != nil {
		return nil, err
	}

	var out []problem
	for _, rel := range files {
		file, err := parsed(root, rel)
		if err != nil {
			return nil, err
		}
		name := file.Name.Name
		if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
			out = append(out, problem{rel, "declares `package " + name + "`: a package identifier is lowercase, " +
				"whatever the directory holding it is called"})
		}
	}
	return out, nil
}

// 4. The scaffolding reached by something that ships.
//
// tests/ calls t.Fatal, reads fixtures and exists to be linked into a test
// binary. One import of it from a file the compiler puts in the application --
// a command reaching for a helper that already does the thing -- and the
// testing package and the fixture wiring are inside what gets deployed.
//
// The whole subtree counts, not the base package alone. tests/Helpers is the
// directory this rule is named after and the only case that is even reachable:
// tests/Helpers holds ordinary packages, so importing one compiles, while the
// base imports nothing of the application and cannot close a cycle either way.
// A check matching the base path exactly is a check that cannot catch the one
// import it exists for.
//
// Production is what is left after the suite: every .go file that is not a
// _test.go one and does not itself live under tests/. Widening the match
// without narrowing the subject makes the check report the suite for importing
// its own helpers, which is what a suite is supposed to do. Asking
// `go list -deps ./...` gets it wrong from the other end: that pattern matches
// tests/ itself, and -deps prints the packages it was named along with
// everything they import, so the answer is always yes.
//
// The module path is read from go.mod and a failure to read it is an error. A
// check that cannot tell which imports are its own module's and reports nothing
// is a check that passes because it did not run.
func scaffoldingInProduction(root string) ([]problem, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	files, err := subjects(root, "file that ships", func(rel string) bool {
		return !isTestFile(rel) && rel != "tests" && !strings.HasPrefix(rel, "tests/")
	})
	if err != nil {
		return nil, err
	}

	scaffolding := module + "/tests"
	var out []problem
	for _, rel := range files {
		file, err := parsed(root, rel)
		if err != nil {
			return nil, err
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path != scaffolding && !strings.HasPrefix(path, scaffolding+"/") {
				continue
			}
			out = append(out, problem{rel, "imports " + path + ", which is test scaffolding: " +
				"it would be linked into the binary, testing package and all"})
		}
	}
	return out, nil
}

// modulePath reads the module line of the go.mod at root.
func modulePath(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading the module path: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod declares no module path", root)
}

// checks is the guard, named so a failure says which one spoke.
var checks = []struct {
	name string
	// reads says whether the check parses what is inside a file. The one that
	// does not reasons about names and paths, which are as readable in a file
	// that does not compile as in one that does.
	reads bool
	run   func(root string) ([]problem, error)
}{
	{"a test go test does not run", true, invisibleTests},
	{"a test outside tests/ without the _internal suffix", false, misplacedTests},
	{"a capitalised package clause", true, capitalisedPackages},
	{"test scaffolding reached by something that ships", true, scaffoldingInProduction},
}

// TestTheLayoutIsWhatItClaims runs the guard over this repository.
func TestTheLayoutIsWhatItClaims(t *testing.T) {
	root := tests.Root(t)
	for _, c := range checks {
		found, err := c.run(root)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for _, p := range found {
			t.Errorf("%s: %s", c.name, p)
		}
	}
}

// TestTheGuardCatchesWhatItIsFor plants each mistake and watches it be caught.
//
// A guard is accepted because it passed, and a guard that passes on everything
// passes too. Every check above was written against a tree that already
// satisfied it, which is how six defects shipped in a version of this guard: a
// name pattern that missed view_Test.go, a path anchor that let
// app/Services/tests/ through, a scaffolding check asking a question whose
// answer was always yes, a scaffolding check that could not match the one
// directory the rule is named after, a walk that reported success when it read
// nothing, and a check that reported success when the walk read plenty and none
// of it was its business.
func TestTheGuardCatchesWhatItIsFor(t *testing.T) {
	root := plant(t)

	for _, c := range []struct {
		name  string
		run   func(root string) ([]problem, error)
		wants []string
	}{
		{"a test go test does not run", invisibleTests, []string{
			"app/ButtonTest.go",   // the name, with a letter before Test
			"app/view_Test.go",    // the name, with an underscore before Test
			"app/TestBroker.go",   // the contents, under a name no pattern reaches
			"app/benchmarks.go",   // a Benchmark counts, and its name says nothing
			"app/fuzzcorpus.go",   // so does a Fuzz
			"app/Handler_Test.go", // both reasons at once
		}},
		{"a test outside tests/ without the _internal suffix", misplacedTests, []string{
			"app/dispatch_test.go",             // beside the code, no suffix
			"app/Services/tests/order_test.go", // a directory called tests that is not the suite
		}},
		{"a capitalised package clause", capitalisedPackages, []string{
			"app/Cap.go",
		}},
		{"test scaffolding reached by something that ships", scaffoldingInProduction, []string{
			"app/wire.go",   // the base package
			"app/helped.go", // tests/Helpers, which is what the rule is named after
			"cmd/serve.go",  // an aliased import is the same import
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			found, err := c.run(root)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			caught := map[string]bool{}
			for _, p := range found {
				caught[p.path] = true
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

// TestTheGuardFailsWhenItCannotRead is the bug that outlived all the others:
// every check answered "nothing wrong" when it had read nothing at all.
//
// It came back a second time in a narrower form, which is why the middle case
// below exists. Each check is about a different part of the tree, and a walk
// that reads nine hundred files can still hand one of them none of the kind it
// is about -- "every _test.go file is under tests/" is true of a tree with no
// test in it, and reports the same green as a tree that passes.
func TestTheGuardFailsWhenItCannotRead(t *testing.T) {
	empty := t.TempDir()
	for _, c := range checks {
		if _, err := c.run(empty); err == nil {
			t.Errorf("%s: a directory with no Go in it and no go.mod was reported clean", c.name)
		}
	}

	// A tree with Go in it, and none of the kind a particular check examines.
	// Named one by one, because "some check complained" would pass on a
	// complaint from the wrong one.
	for _, c := range []struct {
		holds  string
		body   string
		silent []string
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

		for _, check := range checks {
			_, err := check.run(lopsided)
			if contains(c.silent, check.name) && err == nil {
				t.Errorf("a tree holding only %s was reported clean by %q, which had nothing of its own kind to examine",
					c.holds, check.name)
			}
		}
	}

	// A file the parser refuses is the same failure one file down: a check that
	// skips it has concluded nothing about it and says everything is fine.
	//
	// Asked of each check in the direction that check claims: the three that
	// read contents have to fail, and the one that reasons about names and
	// paths has to succeed, because a name is as readable in a file that does
	// not compile as in one that does.
	// The tree is whole apart from the one file, so each check has something of
	// its own kind to look at and the emptiness refusal above is not what
	// answers here.
	broken := t.TempDir()
	write(t, broken, "go.mod", "module example.test/broken\n")
	write(t, broken, "app/half.go", "package app\n\nfunc Open( {\n")
	write(t, broken, "tests/Unit/whole_test.go", "package unit_test\n")
	for _, c := range checks {
		if _, err := c.run(broken); c.reads != (err != nil) {
			t.Errorf("%s: reads contents = %t, and the file that does not parse produced err = %v", c.name, c.reads, err)
		}
	}
}

// plant builds a tree holding one of each mistake, and the correct shapes that
// each check must stay quiet about.
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
	// Not handler_test.go, which is the name that first suggests itself: the
	// default filesystem on macOS is case-insensitive, so it and the
	// Handler_Test.go above are one file, and whichever is written second
	// silently replaces the contents of the first under the first one's name.
	// Two probes, one of them gone, and the check they belong to reported clean.
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
