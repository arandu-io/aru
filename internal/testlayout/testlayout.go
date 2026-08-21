// Package testlayout checks that a Go module's tests are where they can run.
//
// Four checks over a directory tree, in the order they matter. The first is the
// reason the rest exist: go test runs a file only when its name ends in
// _test.go, so a file one letter away from that pattern compiles into its
// package as ordinary code and every test inside it is skipped -- no error, no
// warning, and a green build over a suite that never executed. Nothing else
// here can fail that quietly.
//
// It answers about two different trees, which is why it is a package rather
// than a body of test code. This repository's own suite is one subject, and the
// application the generator writes is the other; the same four statements are
// true of both, and a second implementation of them is how the two would come
// to disagree.
//
// What it does not do is decide what an empty answer means. A check reports how
// many files it examined, and zero is a fact rather than a verdict: in a
// repository whose suite is required to exist it means the walk is looking in
// the wrong place, and in an application somebody generated this morning it
// means they have not written a test yet.
package testlayout

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Problem is one thing a check found: where it is, what breaks, and why that
// matters.
type Problem struct {
	// Rule names the check, so a report can be grouped and a finding suppressed
	// by something other than its wording.
	Rule string
	// Path is the file, relative to the root that was checked, with forward
	// slashes on every platform.
	Path string
	// Line is where in the file, or 1 when the problem is the file itself.
	Line int
	// Says is what is wrong, in one sentence.
	Says string
	// Why is what it costs. A finding that only says what is forbidden gets
	// suppressed; one that says what breaks gets fixed.
	Why string
}

// String renders the problem as one line.
func (p Problem) String() string { return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Says) }

// Result is what one check saw.
type Result struct {
	// Examined is how many files the check looked at.
	//
	// It is reported because every statement these checks make is of the form
	// "every X is Y", and every one of them is true of no X at all. A caller
	// that requires its subject to exist -- a repository whose suite is not
	// optional -- has to be able to tell "nothing wrong" from "nothing there",
	// and in a log the two are the same green.
	Examined int
	// Problems is what it found.
	Problems []Problem
	// Unreadable is the files the parser refused, if any.
	//
	// Carried rather than turned into an error, because the two callers want
	// opposite things from it: a suite treats a file that does not compile as a
	// failure, and a report that already says "this file does not parse" in its
	// own first rule does not want it said twice.
	Unreadable []string
}

// Check is one of the four.
type Check struct {
	// Name is what the check is about, phrased as the problem it finds.
	Name string
	// Reads says whether the check parses what is inside a file. The one that
	// does not reasons about names and paths, which are as readable in a file
	// that does not compile as in one that does.
	Reads bool
	// Run performs it over a directory tree. The error is for a walk that
	// failed, not for a file that is wrong.
	Run func(root string) (Result, error)
}

// Checks is the guard, in the order the checks matter.
func Checks() []Check {
	return []Check{
		{"a test go test does not run", true, InvisibleTests},
		{"a test outside tests/ without the _internal suffix", false, MisplacedTests},
		{"a capitalised package clause", true, CapitalisedPackages},
		{"test scaffolding reached by something that ships", true, ScaffoldingInProduction},
	}
}

// Run performs every check over root and returns what they found together.
//
// The results are per check as well, because a caller that requires a subject
// to exist needs each Examined separately: three checks that looked at nine
// hundred files do not make up for the fourth that looked at none.
func Run(root string) ([]Problem, map[string]Result, error) {
	var all []Problem
	byCheck := map[string]Result{}
	for _, c := range Checks() {
		result, err := c.Run(root)
		if err != nil {
			return nil, nil, err
		}
		byCheck[c.Name] = result
		all = append(all, result.Problems...)
	}
	return all, byCheck, nil
}

// goFiles is every .go file under root that the go command compiles, as a slash
// path relative to root.
//
// The skipped directories are the ones the go command itself will not build
// from, plus testdata, which it refuses to load at all -- so nothing in there
// is compiled and nothing in there can be a test that silently does not run.
// This repository keeps Go under testdata that does not parse on purpose, for
// the doctor to have something broken to find.
//
// A view is skipped too. It ends in .go and is not Go: the kyse build tag keeps
// the compiler away from the markup below its package clause.
func goFiles(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "bin":
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
	return found, nil
}

// subjects is the files one check examines.
func subjects(root string, keep func(rel string) bool) ([]string, error) {
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
	return out, nil
}

// isTestFile reports whether go test would run the file rather than compile it
// into the package.
func isTestFile(rel string) bool { return strings.HasSuffix(rel, "_test.go") }

// parse reads one file as Go.
func parse(root, rel string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, parser.SkipObjectResolution)
}

// lineOf is the 1-based line a position falls on within a file's contents.
//
// The files are parsed one at a time with a FileSet of their own, so a position
// is an offset into this file and nothing else.
func lineOf(root, rel string, pos token.Pos) int {
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return 1
	}
	offset := int(pos) - 1
	if offset < 0 || offset > len(body) {
		return 1
	}
	return 1 + strings.Count(string(body[:offset]), "\n")
}

// InvisibleTests finds a test go test will not run.
//
// Two ways in, and both are checked because each misses the other.
//
// The name. A file called ButtonTest.go is compiled into its package and run by
// nothing. So is view_Test.go, and it is the one a pattern gets wrong: written
// as [A-Za-z]Test\.go it catches the first and lets the second through, because
// the character before Test is an underscore. Every .go file whose name ends in
// Test.go is reported, which cannot collide with a real one -- _test.go ends in
// a lowercase t.
//
// The contents. A file called TestBroker.go, BrokerTests.go or broker.go fails
// in exactly the same way once a test function is inside it, and no pattern
// over names reaches any of them.
//
// The contents are read as declarations and not as text, which matters: a
// generator carries the source of the test file it emits, func TestEvery and
// all, inside a string literal. A search over text reports it, and that is the
// one place where a test function in a non-test file is exactly right.
func InvisibleTests(root string) (Result, error) {
	files, err := subjects(root, func(rel string) bool { return !isTestFile(rel) })
	if err != nil {
		return Result{}, err
	}

	result := Result{Examined: len(files)}
	for _, rel := range files {
		if strings.HasSuffix(rel, "Test.go") {
			result.Problems = append(result.Problems, Problem{
				Rule: "test-is-not-run", Path: rel, Line: 1,
				Says: "this file is named Test.go, and go test runs only files whose names end in _test.go",
				Why: "whatever is inside it is compiled into the package and executed by nothing -- no error, no warning, " +
					"and a green build over tests that never ran. Rename it to end in _test.go.",
			})
			continue
		}

		file, err := parse(root, rel)
		if err != nil {
			result.Unreadable = append(result.Unreadable, rel)
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isRunnableTest(fn) {
				continue
			}
			result.Problems = append(result.Problems, Problem{
				Rule: "test-is-not-run", Path: rel, Line: lineOf(root, rel, fn.Pos()),
				Says: "this file declares " + fn.Name.Name + " and its name does not end in _test.go",
				Why: "go test compiles the file into the package and runs nothing in it, so the test reports neither " +
					"pass nor fail and the count of tests executed is the only place it shows. Rename the file to end in _test.go.",
			})
		}
	}
	return result, nil
}

// isRunnableTest reports whether go test would run this function.
//
// Go's own rule, and not an approximation of it: one of the three prefixes,
// then either nothing or a rune that is not lower case -- Testx is an ordinary
// function and TestX is not -- with no receiver, one parameter of the matching
// testing type, and no results.
//
// The parameter is what separates a test from a helper. A test base package
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

// MisplacedTests finds a test that uses only the exported API and stayed beside
// the code.
//
// The suffix is the whole exception, and it is not a preference: a test that
// reads an unexported identifier cannot live in another package, Go decides
// that. So it stays beside the code and says so in its name, which turns "it is
// next to the code" into a claim a tool can check rather than a habit nobody
// notices.
//
// "tests/" means tests/ of the module the file belongs to, so the path is
// measured from the nearest go.mod at or above it. Two ways of writing that are
// wrong, and the difference is not cosmetic:
//
//	^tests/       anchored at the project root. A nested module keeps its own
//	              tests/, and every file in it is reported as misplaced
//	(^|/)tests/   anchored at any element. app/Services/tests/order_test.go
//	              passes, and it is nobody's suite
func MisplacedTests(root string) (Result, error) {
	files, err := subjects(root, isTestFile)
	if err != nil {
		return Result{}, err
	}

	result := Result{Examined: len(files)}
	for _, rel := range files {
		if strings.HasSuffix(rel, "_internal_test.go") {
			continue
		}
		inModule, err := relativeToModule(root, rel)
		if err != nil {
			return Result{}, err
		}
		if strings.HasPrefix(inModule, "tests/") {
			continue
		}
		result.Problems = append(result.Problems, Problem{
			Rule: "test-outside-the-tests-tree", Path: rel, Line: 1,
			Says: "this test is outside tests/ and its name does not end in _internal_test.go",
			Why: "a test beside the code is a claim that it reads something the package does not export, which is the " +
				"one reason Go leaves no choice about. Name it _internal_test.go if that is true, and move it under " +
				"tests/ if it is not -- otherwise every listing of the package is doubled by files nobody opens on purpose.",
		})
	}
	return result, nil
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

// CapitalisedPackages finds a package clause that is not lowercase.
//
// The directories under tests/ are capitalised and the package clauses inside
// them are not, and that is not an inconsistency to tidy up: a directory name
// is a label, an identifier is code. Every import of a capitalised package
// reads as an exported name that is not one, and go vet has nothing to say
// about it.
func CapitalisedPackages(root string) (Result, error) {
	files, err := subjects(root, func(string) bool { return true })
	if err != nil {
		return Result{}, err
	}

	result := Result{Examined: len(files)}
	for _, rel := range files {
		file, err := parse(root, rel)
		if err != nil {
			result.Unreadable = append(result.Unreadable, rel)
			continue
		}
		name := file.Name.Name
		if r, _ := utf8.DecodeRuneInString(name); !unicode.IsUpper(r) {
			continue
		}
		result.Problems = append(result.Problems, Problem{
			Rule: "package-clause-is-capitalised", Path: rel, Line: lineOf(root, rel, file.Name.Pos()),
			Says: "this file declares `package " + name + "`",
			Why: "a package identifier is lowercase whatever the directory holding it is called, and every import of a " +
				"capitalised one reads as an exported name that is not one. go vet has nothing to say about it.",
		})
	}
	return result, nil
}

// ScaffoldingInProduction finds the test scaffolding reached by something that
// ships.
//
// A tests/ tree calls t.Fatal, reads fixtures and exists to be linked into a
// test binary. One import of it from a file the compiler puts in the
// application -- a handler reaching for a helper that already does the thing --
// and the testing package and the fixture wiring are inside what gets deployed.
//
// The whole subtree counts, not the base package alone. tests/Helpers is the
// directory this is named after and the only case that is even reachable:
// Helpers holds ordinary packages, so importing one compiles, while a base that
// imports nothing of the application cannot close a cycle either way. Matching
// the base path exactly is a check that cannot catch the one import it exists
// for.
//
// Production is what is left after the suite: every .go file that is not a
// _test.go one and does not itself live under tests/. Widening the match
// without narrowing the subject reports the suite for importing its own
// helpers, which is what a suite is supposed to do. Asking `go list -deps ./...`
// gets it wrong from the other end: that pattern matches tests/ itself, and
// -deps prints the packages it was named along with everything they import, so
// the answer is always yes.
func ScaffoldingInProduction(root string) (Result, error) {
	module, err := ModulePath(root)
	if err != nil {
		return Result{}, err
	}
	files, err := subjects(root, func(rel string) bool {
		return !isTestFile(rel) && rel != "tests" && !strings.HasPrefix(rel, "tests/")
	})
	if err != nil {
		return Result{}, err
	}

	scaffolding := module + "/tests"
	result := Result{Examined: len(files)}
	for _, rel := range files {
		file, err := parse(root, rel)
		if err != nil {
			result.Unreadable = append(result.Unreadable, rel)
			continue
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path != scaffolding && !strings.HasPrefix(path, scaffolding+"/") {
				continue
			}
			result.Problems = append(result.Problems, Problem{
				Rule: "scaffolding-ships", Path: rel, Line: lineOf(root, rel, imp.Pos()),
				Says: "this file imports " + path + ", which is test scaffolding",
				Why: "it is built to be linked into a test binary: it calls t.Fatal, reads fixtures and pulls in the " +
					"testing package. Imported from a file the compiler puts in the application, all of that is inside " +
					"what gets deployed. Move the helper into a package that ships, or call it only from a test.",
			})
		}
	}
	return result, nil
}

// ModulePath reads the module line of the go.mod at root.
//
// A failure to read it is an error rather than an empty answer: a check that
// cannot tell which imports belong to its own module and reports nothing is a
// check that passed because it did not run.
func ModulePath(root string) (string, error) {
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
