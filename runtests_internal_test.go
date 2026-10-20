package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testsProject writes a project with one ordinary test and, when browser is
// set, one browser test, each logging a word only it says, and moves into it.
func testsProject(t *testing.T, browser bool) string {
	t.Helper()
	root := probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	files := map[string]string{
		"tests/Unit/unit_test.go": "package unit\n\nimport \"testing\"\n\nfunc TestUnit(t *testing.T) { t.Log(\"ordinary-suite-ran\") }\n",
	}
	if browser {
		files["tests/Browser/checkout_test.go"] = "//go:build browser\n\npackage browser\n\nimport \"testing\"\n\n" +
			"func TestCheckout(t *testing.T) { t.Log(\"browser-suite-ran\") }\n"
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestTheTestCommandRunsTheOrdinarySuiteWithoutTheBrowserOne runs aru test in
// a project holding both suites and reads which one go test ran. -v is
// typed by the person and only reaches go test if the arguments are handed
// on, so the log lines prove that too.
func TestTheTestCommandRunsTheOrdinarySuiteWithoutTheBrowserOne(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so no test could run")
	}
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "-v")
	if code != 0 {
		t.Fatalf("aru test exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("the ordinary suite did not run, or -v was not handed to go test:\n%s", stdout)
	}
	if strings.Contains(stdout, "browser-suite-ran") {
		t.Errorf("aru test without --browser ran the browser suite, which needs a browser:\n%s", stdout)
	}
}

// TestTheBrowserFlagRunsTheBrowserSuiteAlone is the other half: with
// --browser the tagged package runs, and nothing else does.
func TestTheBrowserFlagRunsTheBrowserSuiteAlone(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so no test could run")
	}
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "--browser", "-v")
	if code != 0 {
		t.Fatalf("aru test --browser exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "browser-suite-ran") {
		t.Errorf("the browser suite did not run:\n%s", stdout)
	}
	if strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("aru test --browser ran the ordinary suite too:\n%s", stdout)
	}
}

// TestTheBrowserFlagSaysWhenThereIsNoBrowserSuite: asking for a suite the
// project does not have is answered with where it would live, not with a
// go test that matched no package.
func TestTheBrowserFlagSaysWhenThereIsNoBrowserSuite(t *testing.T) {
	testsProject(t, false)

	code, _, stderr := exercise(t, "test", "--browser")
	if code == 0 {
		t.Fatal("aru test --browser passed in a project with no tests/Browser")
	}
	for _, want := range []string{"tests/Browser", "//go:build browser", "aru test --browser"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q: %s", want, stderr)
		}
	}
}

// TestAFailingTestFailsTheCommand keeps the exit status: a CI step that runs
// aru test has nothing else to read.
func TestAFailingTestFailsTheCommand(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so no test could run")
	}
	root := testsProject(t, false)
	body := "package unit\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"broken\") }\n"
	if err := os.WriteFile(filepath.Join(root, "tests", "Unit", "broken_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := exercise(t, "test"); code == 0 {
		t.Fatalf("aru test exited zero with a failing test: %s", stderr)
	}
}

// TestTakeBrowserLeavesEverythingElse reads the one piece of argument
// handling the command does: --browser out, wherever it stands before --, and
// the rest untouched and in order.
func TestTakeBrowserLeavesEverythingElse(t *testing.T) {
	for _, c := range []struct {
		in      []string
		browser bool
		rest    []string
	}{
		{nil, false, []string{}},
		{[]string{"-run", "TestX", "--browser", "-v"}, true, []string{"-run", "TestX", "-v"}},
		{[]string{"-browser"}, true, []string{}},
		{[]string{"-v", "--", "--browser"}, false, []string{"-v", "--", "--browser"}},
	} {
		browser, rest := takeBrowser(c.in)
		if browser != c.browser || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("takeBrowser(%q) = %v, %q; want %v, %q", c.in, browser, rest, c.browser, c.rest)
		}
	}
}
