package doctor

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTheListIsWhatTheSourceReports compares List with every finding the
// source can construct.
//
// The list is written by hand, so it is checked the way emitsByRule is: from
// the code, in both directions. A rule added without its line here, a line
// left behind by a deleted rule, and a severity changed in one place and not
// the other all fail, naming the rule.
func TestTheListIsWhatTheSourceReports(t *testing.T) {
	reported := map[string][]Severity{}
	for _, path := range ruleFiles {
		for name, severities := range severitiesInSource(t, path) {
			if _, twice := reported[name]; twice && name != "" {
				t.Errorf("%s is constructed in more than one of %v", name, ruleFiles)
			}
			reported[name] = severities
		}
	}
	forwarded := declaredRuleNames(t, "../testlayout/testlayout.go")
	borrowed, ok := reported[""]
	if !ok || len(forwarded) == 0 {
		t.Fatal("no finding forwards a rule name from internal/testlayout, so the four borrowed names have no severity to compare")
	}
	delete(reported, "")
	for name := range forwarded {
		reported[name] = borrowed
	}

	listed := map[string][]Severity{}
	for _, r := range List() {
		if _, twice := listed[r.Name]; twice {
			t.Errorf("%s is listed twice", r.Name)
		}
		listed[r.Name] = r.Severities
	}
	for name, severities := range reported {
		got, ok := listed[name]
		if !ok {
			t.Errorf("the source reports %s and List does not name it", name)
			continue
		}
		if !slices.Equal(got, severities) {
			t.Errorf("List says %s reports at %v and the source reports it at %v", name, got, severities)
		}
	}
	for name := range listed {
		if _, ok := reported[name]; !ok {
			t.Errorf("List names %s and nothing in the source reports it", name)
		}
	}

	// And the order: List reads in the order the rules run, which is what lets
	// a person find a rule in it next to the ones it is checked beside.
	function := map[string]string{}
	for fn, names := range emitsByRule() {
		for _, name := range names {
			function[name] = fn
		}
	}
	position := map[string]int{}
	for i, rule := range rules {
		name := runtime.FuncForPC(reflect.ValueOf(rule).Pointer()).Name()
		position[name[strings.LastIndex(name, ".")+1:]] = i
	}
	last := -1
	for _, r := range List() {
		fn, known := function[r.Name]
		if !known {
			t.Errorf("%s is listed and no rule in emitsByRule reports it", r.Name)
			continue
		}
		if position[fn] < last {
			t.Errorf("%s is listed after a rule that runs later than %s", r.Name, fn)
		}
		last = position[fn]
	}
}

// TestAProfileRuleIsListedWithItsProfile runs every fixture on the
// conventional profile and the clean one on the performance profile: a rule
// listed as one profile's fires only there, and every one that fires only
// there is listed as such.
func TestAProfileRuleIsListedWithItsProfile(t *testing.T) {
	conventional := map[string]bool{}
	for _, dir := range []string{"testdata/violations", "testdata/gaps", "testdata/broken", "testdata/clean"} {
		findings, err := Run(dir, Conventional)
		if err != nil {
			t.Fatalf("Run %s: %v", dir, err)
		}
		for _, f := range findings {
			conventional[f.Rule] = true
		}
	}
	performance, err := Run("testdata/clean", Performance)
	if err != nil {
		t.Fatalf("Run on the performance profile: %v", err)
	}

	for _, r := range List() {
		if r.Profile == Performance && conventional[r.Name] {
			t.Errorf("%s is listed as %s only and fired on the conventional profile", r.Name, Performance)
		}
	}
	listed := map[string]Profile{}
	for _, r := range List() {
		listed[r.Name] = r.Profile
	}
	for _, f := range performance {
		if !conventional[f.Rule] && listed[f.Rule] != Performance {
			t.Errorf("%s fired only on the %s profile and is listed for every profile", f.Rule, Performance)
		}
	}
}

// TestWriteListPrintsOneLinePerRule pins the table `aru doctor --list` prints:
// every rule on its own line, its severity in words, and the profile a rule is
// limited to.
func TestWriteListPrintsOneLinePerRule(t *testing.T) {
	var out bytes.Buffer
	if err := WriteList(&out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(List()) {
		t.Fatalf("%d lines for %d rules:\n%s", len(lines), len(List()), out.String())
	}
	columns := regexp.MustCompile(` {2,}`)
	for _, want := range []string{
		"enum-rule-not-derived | error or warning",
		"policy-never-opened | warning",
		"join-across-aggregates | error | --profile=performance only",
	} {
		found := false
		for _, line := range lines {
			if strings.Join(columns.Split(strings.TrimSpace(line), -1), " | ") == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no line reads %q:\n%s", want, out.String())
		}
	}
}

// severitiesInSource reads every Finding literal in a file and answers, per
// rule name, the severities it is constructed with, error first. A literal
// whose rule is not a string -- a name forwarded from another package -- is
// recorded under the empty name.
func severitiesInSource(t *testing.T, path string) map[string][]Severity {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	seen := map[string]map[Severity]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var name string
		var severity *Severity
		isRule := false
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Rule":
				isRule = true
				if s, ok := kv.Value.(*ast.BasicLit); ok && s.Kind == token.STRING {
					name, _ = strconv.Unquote(s.Value)
				}
			case "Severity":
				if id, ok := kv.Value.(*ast.Ident); ok {
					switch id.Name {
					case "Error":
						s := Error
						severity = &s
					case "Warning":
						s := Warning
						severity = &s
					}
				}
			}
		}
		if !isRule || severity == nil {
			return true
		}
		if seen[name] == nil {
			seen[name] = map[Severity]bool{}
		}
		seen[name][*severity] = true
		return true
	})

	out := map[string][]Severity{}
	for name, set := range seen {
		var severities []Severity
		for s := range set {
			severities = append(severities, s)
		}
		sort.Slice(severities, func(i, j int) bool { return severities[i] > severities[j] })
		out[name] = severities
	}
	return out
}
