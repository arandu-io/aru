package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testsProject writes a project with one ordinary test and, when e2e is set,
// two journeys -- one smoke, one not -- each logging a word only it says and
// the driver and engine it was handed, and moves into it.
func testsProject(t *testing.T, e2e bool) string {
	t.Helper()
	root := probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	files := map[string]string{
		"tests/Unit/unit_test.go": "package unit\n\nimport \"testing\"\n\nfunc TestUnit(t *testing.T) { t.Log(\"ordinary-suite-ran\") }\n",
	}
	if e2e {
		files["tests/E2E/checkout_test.go"] = "//go:build e2e\n\npackage e2e_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
			"func TestCheckout(t *testing.T) { t.Log(\"e2e-suite-ran\", \"driver=\"+os.Getenv(\"ARANDU_E2E_DRIVER\"), \"engine=\"+os.Getenv(\"ARANDU_E2E_BROWSER\")) }\n\n" +
			"func TestSmokeHome(t *testing.T) { t.Log(\"smoke-ran\") }\n"
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

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so no test could run")
	}
}

// TestTheTestCommandRunsTheOrdinarySuiteWithoutTheJourneys runs aru test in a
// project holding both suites and reads which one go test ran. -v is typed by
// the person and only reaches go test if the arguments are handed on, so the
// log lines prove that too.
func TestTheTestCommandRunsTheOrdinarySuiteWithoutTheJourneys(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "-v")
	if code != 0 {
		t.Fatalf("aru test exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("the ordinary suite did not run, or -v was not handed to go test:\n%s", stdout)
	}
	if strings.Contains(stdout, "e2e-suite-ran") {
		t.Errorf("aru test without --e2e ran the journeys, which need a browser:\n%s", stdout)
	}
}

// TestTheE2EFlagRunsTheJourneysAlone is the other half: with --e2e the tagged
// package runs, and nothing else does.
func TestTheE2EFlagRunsTheJourneysAlone(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "--e2e", "-v")
	if code != 0 {
		t.Fatalf("aru test --e2e exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "e2e-suite-ran") || !strings.Contains(stdout, "smoke-ran") {
		t.Errorf("the journeys did not run:\n%s", stdout)
	}
	if strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("aru test --e2e ran the ordinary suite too:\n%s", stdout)
	}
}

// TestTheBareBrowserFlagIsTheOldNameOfE2E keeps the old spelling working, with
// a notice that names the new one.
func TestTheBareBrowserFlagIsTheOldNameOfE2E(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "--browser", "-v")
	if code != 0 {
		t.Fatalf("aru test --browser exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "e2e-suite-ran") || strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("--browser did not run tests/E2E alone:\n%s", stdout)
	}
	if !strings.Contains(stderr, "write --e2e") {
		t.Errorf("--browser ran without saying it is the old name of --e2e: %s", stderr)
	}
}

// TestSmokeRunsOnlyTheSmokeJourneys reads the convention: a smoke journey is
// a test whose name starts with TestSmoke.
func TestSmokeRunsOnlyTheSmokeJourneys(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "--smoke", "-v")
	if code != 0 {
		t.Fatalf("aru test --smoke exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "smoke-ran") || strings.Contains(stdout, "e2e-suite-ran") {
		t.Errorf("--smoke did not run the smoke journey alone:\n%s", stdout)
	}
	if code, _, stderr := exercise(t, "test", "--smoke", "-run", "X"); code == 0 || !strings.Contains(stderr, "drop -run") {
		t.Errorf("--smoke with a -run of its own was not refused: %s", stderr)
	}
}

// TestDriverAndBrowserReachTheJourneys reads the two variables inside the
// test go test ran.
func TestDriverAndBrowserReachTheJourneys(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	code, stdout, stderr := exercise(t, "test", "--driver=playwright", "--browser", "firefox", "-run", "TestCheckout", "-v")
	if code != 0 {
		t.Fatalf("aru test exited %d: %s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "driver=playwright") || !strings.Contains(stdout, "engine=firefox") {
		t.Errorf("the journey did not read ARANDU_E2E_DRIVER and ARANDU_E2E_BROWSER:\n%s", stdout)
	}
}

// TestShardsSplitTheSuiteWithoutOverlap runs both shards of two and reads
// that every journey ran once, in exactly one of them.
func TestShardsSplitTheSuiteWithoutOverlap(t *testing.T) {
	needGo(t)
	testsProject(t, true)

	ran := map[string]int{}
	for _, shard := range []string{"1/2", "2/2"} {
		code, stdout, stderr := exercise(t, "test", "--e2e", "--shard="+shard, "-v")
		if code != 0 {
			t.Fatalf("aru test --shard=%s exited %d: %s\n%s", shard, code, stdout, stderr)
		}
		for _, name := range []string{"TestCheckout", "TestSmokeHome"} {
			if strings.Contains(stdout, "--- PASS: "+name) {
				ran[name]++
			}
		}
	}
	if ran["TestCheckout"] != 1 || ran["TestSmokeHome"] != 1 {
		t.Errorf("two shards ran the journeys %v times; each should run once", ran)
	}
	if code, _, stderr := exercise(t, "test", "--shard=3/2"); code == 0 || !strings.Contains(stderr, "1 <= i <= n") {
		t.Errorf("--shard=3/2 was not refused: %s", stderr)
	}
}

// TestTheE2EFlagSaysWhenThereIsNoE2ESuite: asking for a suite the project does
// not have is answered with where it would live, not with a go test that
// matched no package -- and a project still holding tests/Browser is told to
// move it.
func TestTheE2EFlagSaysWhenThereIsNoE2ESuite(t *testing.T) {
	root := testsProject(t, false)

	code, _, stderr := exercise(t, "test", "--e2e")
	if code == 0 {
		t.Fatal("aru test --e2e passed in a project with no tests/E2E")
	}
	for _, want := range []string{"tests/E2E", "//go:build e2e", "aru make:e2e"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q: %s", want, stderr)
		}
	}

	if err := os.MkdirAll(filepath.Join(root, "tests", "Browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := exercise(t, "test", "--e2e"); code == 0 || !strings.Contains(stderr, "move the package there") {
		t.Errorf("a project with tests/Browser was not told to move it: %s", stderr)
	}
}

// TestAFailingTestFailsTheCommand keeps the exit status: a CI step that runs
// aru test has nothing else to read.
func TestAFailingTestFailsTheCommand(t *testing.T) {
	needGo(t)
	root := testsProject(t, false)
	body := "package unit\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"broken\") }\n"
	if err := os.WriteFile(filepath.Join(root, "tests", "Unit", "broken_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := exercise(t, "test"); code == 0 {
		t.Fatalf("aru test exited zero with a failing test: %s", stderr)
	}
}

// TestParseTestArgsLeavesEverythingElse reads the argument handling: aru
// test's own flags out, wherever they stand before --, and the rest untouched
// and in order.
func TestParseTestArgsLeavesEverythingElse(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want testRun
	}{
		{nil, testRun{rest: []string{}}},
		{[]string{"-run", "TestX", "--browser", "-v"}, testRun{e2e: true, deprecated: true, rest: []string{"-run", "TestX", "-v"}}},
		{[]string{"-browser=webkit"}, testRun{e2e: true, engine: "webkit", rest: []string{}}},
		{[]string{"--smoke", "--shard", "2/3"}, testRun{e2e: true, smoke: true, shard: 2, shards: 3, rest: []string{}}},
		{[]string{"--driver", "cdp", "-count=1"}, testRun{e2e: true, driver: "cdp", rest: []string{"-count=1"}}},
		{[]string{"-v", "--", "--e2e"}, testRun{rest: []string{"-v", "--", "--e2e"}}},
	} {
		got, err := parseTestArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseTestArgs(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range [][]string{{"--browser=netscape"}, {"--shard=0/2"}, {"--driver"}} {
		if _, err := parseTestArgs(bad); err == nil {
			t.Errorf("parseTestArgs(%q) accepted it", bad)
		}
	}
	if rest, tags := takeTags([]string{"-tags", "pg,slow", "-v", "-tags=x"}); !reflect.DeepEqual(tags, []string{"pg", "slow", "x"}) || !reflect.DeepEqual(rest, []string{"-v"}) {
		t.Errorf("takeTags = %q, %q", rest, tags)
	}
}
