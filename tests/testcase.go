// Package tests is the base every suite in this repository builds on.
//
// It is a package the suites import rather than a base class they extend. What
// belongs here is what more than one suite needs; a helper used by one test
// belongs beside that test.
//
// The suites mean what their names say:
//
//	tests/Unit/  checks one thing, with nothing running
//	tests/Fuzz/  feeds arbitrary bytes at a target, with its corpus beside it
//
// Each holds one directory per package under test, so tests/Unit/gen answers
// for internal/gen. The tree is mirrored rather than flat because a flat one is
// a single Go package: two suites that each want a helper called write, or a
// suite whose helper is called spec next to one that imports the package spec,
// collide on a name that has nothing to do with either of them.
//
// The file is testcase.go, lowercase, because it is a package and not a test.
// Only a file whose name ends in _test.go is run by go test: a TestCase.go one
// letter away from that pattern teaches a name that, applied to a file with
// test functions in it, compiles into the package and runs nothing -- no error,
// no warning, and a green build over a suite that never executed.
package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// Root is the directory holding the module's go.mod.
//
// It is found by walking up rather than by counting "../.." levels, because the
// suites do not all sit at the same depth: tests/Unit/gen is three below the
// root and tests/Unit is two, and a fixed count silently reads the wrong
// directory the day a suite gains one.
//
// It fails rather than answering with a guess. Every caller uses the result to
// reach a fixture, and a wrong root turns "the fixture says X" into "the file
// was not there", which reads as a broken test rather than a broken path.
func Root(tb testing.TB) string {
	tb.Helper()

	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("finding the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatalf("no go.mod at or above %s: tests.Root cannot say where the project is", dir)
		}
		dir = parent
	}
}

// Fixture is the path of a directory of test data belonging to a package.
//
// The fixtures stay beside the code they describe -- internal/doctor/testdata
// holds a project that does not parse because that is a fact about the doctor,
// not about this suite -- and one of them is read from two places at once: the
// rule set is enumerated by a test that cannot leave internal/doctor, and the
// report is read by a test that cannot stay there. Naming the arrangement once
// is what keeps the second copy of it from drifting.
//
// The directory has to be testdata, whatever else the path says. It is the one
// name the go command refuses to build, load or vet, and the fixtures here
// include Go that does not compile on purpose.
func Fixture(tb testing.TB, pkg string, parts ...string) string {
	tb.Helper()

	path := filepath.Join(append([]string{Root(tb), "internal", pkg, "testdata"}, parts...)...)
	if _, err := os.Stat(path); err != nil {
		tb.Fatalf("fixture %s: %v", path, err)
	}
	return path
}
