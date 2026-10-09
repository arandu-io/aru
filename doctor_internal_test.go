package main

import (
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
