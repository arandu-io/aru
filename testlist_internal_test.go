package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/testlayout"
)

// listProject is testsProject with the end-to-end journeys, a conformance
// test and a table test, and with no go on PATH: listing reads the sources,
// and a listing that started the toolchain fails here.
func listProject(t *testing.T) string {
	t.Helper()
	root := testsProject(t, true)
	body := "package conformance\n\nimport \"testing\"\n\nfunc TestKit(t *testing.T) {\n" +
		"\tt.Run(\"sessions\", func(t *testing.T) {\n\t\tt.Run(\"expire\", func(t *testing.T) {})\n\t})\n}\n"
	path := filepath.Join(root, "tests", "Conformance", "kit_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	return root
}

// TestTheListIsTheJSONTheHelpDocuments reads aru test --list --json the way
// the editor does: every key of every object is one the help names, the
// journeys carry the tag they need, and the conformance test is in its layer.
func TestTheListIsTheJSONTheHelpDocuments(t *testing.T) {
	listProject(t)

	for _, flag := range []string{"--json", "-json"} {
		code, stdout, stderr := exercise(t, "test", "--list", flag)
		if code != 0 {
			t.Fatalf("aru test --list %s exited %d: %s", flag, code, stderr)
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			t.Fatalf("aru test --list %s is not JSON: %v\n%s", flag, err, stdout)
		}
		keys := func(m any) []string {
			var out []string
			for k := range m.(map[string]any) {
				out = append(out, k)
			}
			sort.Strings(out)
			return out
		}
		if got := keys(raw); !reflect.DeepEqual(got, []string{"layers", "module", "tests", "unreadable", "version"}) {
			t.Errorf("top-level keys %q", got)
		}
		testKeys := []string{"constraint", "file", "layer", "line", "name", "package", "subtests", "tags"}
		for _, test := range raw["tests"].([]any) {
			if got := keys(test); !reflect.DeepEqual(got, testKeys) {
				t.Errorf("a test has keys %q; want %q", got, testKeys)
			}
		}

		var inv testlayout.Inventory
		if err := json.Unmarshal([]byte(stdout), &inv); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, test := range inv.Tests {
			var subs []string
			for _, s := range test.Subtests {
				subs = append(subs, s.Name)
			}
			got[test.Name] = test.Layer + " " + test.Package + " " + test.File + ":" + strconv.Itoa(test.Line) + " [" + strings.Join(test.Tags, ",") + "] " +
				strings.Join(subs, ",")
		}
		want := map[string]string{
			"TestUnit":      "Unit example.test/probe/tests/Unit tests/Unit/unit_test.go:5 [] ",
			"TestCheckout":  "E2E example.test/probe/tests/E2E tests/E2E/checkout_test.go:10 [e2e] ",
			"TestSmokeHome": "E2E example.test/probe/tests/E2E tests/E2E/checkout_test.go:12 [e2e] ",
			"TestKit":       "Conformance example.test/probe/tests/Conformance tests/Conformance/kit_test.go:5 [] TestKit/sessions,TestKit/sessions/expire",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("aru test --list %s =\n%q\nwant\n%q", flag, got, want)
		}
		if inv.Version != 1 {
			t.Errorf("version %d", inv.Version)
		}
	}
}

// TestTheHelpNamesEveryFieldOfTheSchema keeps the documented schema and the
// encoded one together: a field added to the inventory and not to the help is
// a field no editor author can find.
func TestTheHelpNamesEveryFieldOfTheSchema(t *testing.T) {
	_, schema, found := strings.Cut(testHelp, "--list --json")
	if !found {
		t.Fatal("aru test --help does not describe --list --json")
	}
	for _, v := range []any{testlayout.Inventory{}, testlayout.Layer{}, testlayout.Test{}, testlayout.Subtest{}, testlayout.Unreadable{}} {
		ty := reflect.TypeOf(v)
		for i := 0; i < ty.NumField(); i++ {
			name, _, _ := strings.Cut(ty.Field(i).Tag.Get("json"), ",")
			if !regexp.MustCompile(`\b` + name + `\b`).MatchString(schema) {
				t.Errorf("%s.%s is encoded as %q and aru test --help does not name it", ty.Name(), ty.Field(i).Name, name)
			}
		}
	}
	if !strings.Contains(schema, "version     1") {
		t.Error("the help does not give the schema's version")
	}
}

// TestTheListIsATableByLayer reads the plain listing: a header per layer that
// has tests, the tag the end-to-end layer needs, each test under its package,
// and the count at the end.
func TestTheListIsATableByLayer(t *testing.T) {
	listProject(t)

	code, stdout, stderr := exercise(t, "test", "--list")
	if code != 0 {
		t.Fatalf("aru test --list exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"Unit (1 test, tests/Unit)\n  example.test/probe/tests/Unit\n    TestUnit",
		"E2E (2 tests, tests/E2E, -tags e2e)\n  example.test/probe/tests/E2E\n",
		"TestCheckout   tests/E2E/checkout_test.go:10  e2e\n",
		"Conformance (1 test, tests/Conformance)\n",
		"      TestKit/sessions         tests/Conformance/kit_test.go:6\n",
		"      TestKit/sessions/expire  tests/Conformance/kit_test.go:7\n",
		"4 tests and 2 subtests in 3 layers\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Feature") || strings.Contains(stdout, "ordinary-suite-ran") {
		t.Errorf("the listing shows an empty layer, or ran a test:\n%s", stdout)
	}
}

// TestTheListRefusesWhatWouldRunTests: --list runs nothing, so a flag that
// chooses what to run, or one meant for go test, is refused by name rather
// than ignored.
func TestTheListRefusesWhatWouldRunTests(t *testing.T) {
	listProject(t)

	for _, args := range [][]string{
		{"--list", "--e2e"}, {"--list", "--smoke"}, {"--list", "--shard=1/2"}, {"--list", "-v"},
		{"--list", "TestX"}, {"--list", "--browser=firefox"},
	} {
		code, _, stderr := exercise(t, append([]string{"test"}, args...)...)
		if code == 0 || !strings.Contains(stderr, "drop "+strings.SplitN(args[1], "=", 2)[0]) {
			t.Errorf("aru test %q was not refused by naming %s: %s", args, args[1], stderr)
		}
	}
}

// TestListAndJSONAreTakenOnlyWhereTheyBelongToAru: --list without a value and
// --json beside it are aru's; -list=<regexp> and a -json without --list are go
// test's, and stay in place.
func TestListAndJSONAreTakenOnlyWhereTheyBelongToAru(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want testRun
	}{
		{[]string{"--list"}, testRun{list: true, rest: []string{}}},
		{[]string{"-json", "--list"}, testRun{list: true, json: true, rest: []string{}}},
		{[]string{"--list", "--json", "-json"}, testRun{list: true, json: true, rest: []string{}}},
		{[]string{"-run", "X", "-json", "-v"}, testRun{rest: []string{"-run", "X", "-json", "-v"}}},
		{[]string{"-list=Test.*", "-json"}, testRun{rest: []string{"-list=Test.*", "-json"}}},
		{[]string{"--list", "-json=true"}, testRun{list: true, rest: []string{"-json=true"}}},
		{[]string{"-v", "--", "--list"}, testRun{rest: []string{"-v", "--", "--list"}}},
	} {
		got, err := parseTestArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseTestArgs(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}
