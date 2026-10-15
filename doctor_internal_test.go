package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/doctor"
)

// TestDoctorListsEveryRule: `aru doctor --list` answers outside a project,
// with one line per rule the doctor checks and the severity each reports at,
// which is what a skill documenting the rules is checked against.
func TestDoctorListsEveryRule(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := exercise(t, "doctor", "--list")
	if code != 0 {
		t.Fatalf("doctor --list exited %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	rules := doctor.List()
	if len(lines) != len(rules) {
		t.Fatalf("%d lines for %d rules:\n%s", len(lines), len(rules), stdout)
	}
	for i, r := range rules {
		fields := strings.Fields(lines[i])
		if len(fields) < 2 || fields[0] != r.Name || !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(lines[i], r.Name)), r.Severity()) {
			t.Errorf("line %d reads %q, want %s and %s", i+1, lines[i], r.Name, r.Severity())
		}
	}
}

// templatePage is a service that reports two warnings and no error --
// html-template-in-app at its import and service-takes-http at its signature
// -- and one helper-reimplemented for each name in brl, each a function with
// BRL in its name.
func templatePage(name string, brl ...string) string {
	body := "package services\n\nimport \"html/template\"\n\nfunc " + name + "() template.HTML { return \"\" }\n"
	for _, fn := range brl {
		body += "\nfunc " + fn + "(cents int) string { return \"\" }\n"
	}
	return body
}

// TestTheBaselineFailsOnlyWhatIsNew walks the ratchet through a project's
// life: the baseline is written from today's warnings, a strict run against it
// passes while printing every one of them, one more warning in a file fails
// it and is named, a file whose warnings went away passes and is named as
// what the baseline can shrink to, and an error fails whatever the baseline
// says.
func TestTheBaselineFailsOnlyWhatIsNew(t *testing.T) {
	root := bareProject(t)
	writeFile(t, filepath.Join(root, "app", "Services", "Page.go"), templatePage("Page", "FormatBRL"))
	writeFile(t, filepath.Join(root, "app", "Services", "Other.go"), templatePage("Other"))
	t.Chdir(root)

	if code, _, _ := exercise(t, "doctor", "--strict"); code == 0 {
		t.Fatal("a strict run with warnings and no baseline passed")
	}

	code, stdout, stderr := exercise(t, "doctor", "--baseline=doctor-baseline.json", "--write-baseline")
	if code != 0 {
		t.Fatalf("writing the baseline exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "wrote doctor-baseline.json: 5 warning(s) in 5 rule and file pair(s)") {
		t.Errorf("the write does not say what it wrote:\n%s", stdout)
	}
	written, err := os.ReadFile(filepath.Join(root, "doctor-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "profile": "conventional",
  "warnings": [
    {"file": "app/Services/Other.go", "rule": "html-template-in-app", "count": 1},
    {"file": "app/Services/Other.go", "rule": "service-takes-http", "count": 1},
    {"file": "app/Services/Page.go", "rule": "helper-reimplemented", "count": 1},
    {"file": "app/Services/Page.go", "rule": "html-template-in-app", "count": 1},
    {"file": "app/Services/Page.go", "rule": "service-takes-http", "count": 1}
  ]
}
`
	if string(written) != want {
		t.Errorf("the baseline is not the sorted file:\n%s\nwant:\n%s", written, want)
	}

	code, stdout, stderr = exercise(t, "doctor", "--strict", "--baseline=doctor-baseline.json")
	if code != 0 {
		t.Fatalf("a strict run within the baseline exited %d: %s\n%s", code, stderr, stdout)
	}
	if strings.Count(stdout, "[html-template-in-app]") != 2 || !strings.Contains(stdout, "5 warning(s), 0 above the baseline, no errors") {
		t.Errorf("the run within the baseline does not print every warning and the count:\n%s", stdout)
	}

	// One more warning in a file the baseline names, and a file it does not.
	writeFile(t, filepath.Join(root, "app", "Services", "Page.go"), templatePage("Page", "FormatBRL", "ParseBRL"))
	writeFile(t, filepath.Join(root, "app", "Services", "Fresh.go"), templatePage("Fresh"))
	code, stdout, stderr = exercise(t, "doctor", "--strict", "--baseline=doctor-baseline.json")
	if code == 0 {
		t.Fatalf("a strict run above the baseline passed:\n%s", stdout)
	}
	for _, line := range []string{
		"above doctor-baseline.json:",
		"  app/Services/Fresh.go [html-template-in-app] 1, baseline 0",
		"  app/Services/Fresh.go [service-takes-http] 1, baseline 0",
		"  app/Services/Page.go [helper-reimplemented] 2, baseline 1",
	} {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("the report does not carry %q:\n%s", line, stdout)
		}
	}
	if !strings.Contains(stderr, "0 error(s), 8 warning(s), 3 above the baseline") {
		t.Errorf("the summary does not count what is new: %s", stderr)
	}
	if code, _, _ := exercise(t, "doctor", "--baseline=doctor-baseline.json"); code != 0 {
		t.Error("a run without --strict failed on warnings")
	}

	// The new warnings go and one of the old ones with them.
	if err := os.Remove(filepath.Join(root, "app", "Services", "Fresh.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "app", "Services", "Page.go"), templatePage("Page", "FormatBRL"))
	if err := os.Remove(filepath.Join(root, "app", "Services", "Other.go")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = exercise(t, "doctor", "--strict", "--baseline=doctor-baseline.json")
	if code != 0 {
		t.Fatalf("a strict run below the baseline failed %d: %s\n%s", code, stderr, stdout)
	}
	for _, line := range []string{
		"below doctor-baseline.json, which can shrink with: aru doctor --baseline=doctor-baseline.json --write-baseline",
		"  app/Services/Other.go [html-template-in-app] 0, baseline 1",
		"  app/Services/Other.go [service-takes-http] 0, baseline 1",
	} {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("the report does not carry %q:\n%s", line, stdout)
		}
	}

	// An error is never in a baseline.
	writeFile(t, filepath.Join(root, "app", "Services", "Broken.go"), "package services\n\nfunc Broken( {\n")
	if code, _, stderr = exercise(t, "doctor", "--strict", "--baseline=doctor-baseline.json"); code == 0 || !strings.Contains(stderr, "1 error(s)") {
		t.Errorf("an error passed against the baseline: %d %s", code, stderr)
	}
	if code, _, _ = exercise(t, "doctor", "--baseline=doctor-baseline.json", "--write-baseline"); code == 0 {
		t.Error("writing the baseline over an error exited 0")
	}
	if body, _ := os.ReadFile(filepath.Join(root, "doctor-baseline.json")); strings.Contains(string(body), "file-does-not-parse") {
		t.Errorf("the baseline recorded an error:\n%s", body)
	}
}

// TestTheBaselineIsRefusedWhenItCannotBeRead: no file, a flag that writes
// with no file named, a file of the other profile and a file edited into a
// shape the command would have to guess about each stop the run before
// anything is checked.
func TestTheBaselineIsRefusedWhenItCannotBeRead(t *testing.T) {
	root := bareProject(t)
	t.Chdir(root)

	for _, tc := range []struct {
		name string
		args []string
		body string
		want string
	}{
		{"missing", []string{"--baseline=nothing.json"}, "", "aru doctor --baseline=nothing.json --write-baseline"},
		{"write with no file", []string{"--write-baseline"}, "", "--write-baseline writes the file --baseline names"},
		{"other profile", []string{"--baseline=b.json", "--profile=performance"}, `{"profile": "conventional", "warnings": []}`, "counts the warnings of the conventional profile, and this run checks performance"},
		{"unknown field", []string{"--baseline=b.json"}, `{"profile": "conventional", "warnings": [], "ignore": ["x"]}`, "unknown field"},
		{"twice", []string{"--baseline=b.json"}, `{"profile": "conventional", "warnings": [{"file": "a.go", "rule": "r", "count": 1}, {"file": "a.go", "rule": "r", "count": 2}]}`, "a.go [r] is written twice"},
		{"zero", []string{"--baseline=b.json"}, `{"profile": "conventional", "warnings": [{"file": "a.go", "rule": "r", "count": 0}]}`, "has a count of 0"},
		{"no profile", []string{"--baseline=b.json"}, `{"warnings": []}`, "unknown profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body != "" {
				writeFile(t, filepath.Join(root, "b.json"), tc.body)
			}
			code, stdout, stderr := exercise(t, append([]string{"doctor"}, tc.args...)...)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Errorf("exited %d, want a refusal naming %q: %s", code, tc.want, stderr)
			}
			if stdout != "" {
				t.Errorf("the project was checked before the baseline was refused:\n%s", stdout)
			}
		})
	}
}
