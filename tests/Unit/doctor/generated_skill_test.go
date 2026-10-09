package doctor_test

import (
	"strings"
	"testing"
)

// skillWithSource is a SKILL.md whose metadata records source, or none when
// source is empty.
func skillWithSource(name, source string) string {
	head := "---\nname: " + name + "\ndescription: The " + name + " module. Use when the request mentions it.\nlicense: MIT\n"
	if source != "" {
		head += "metadata:\n  source: " + source + "\n  digest: sha256:00\n"
	}
	return head + "---\n\n# " + name + "\n"
}

// TestASkillTheGeneratorWroteIsReportedAsRetired: make:module no longer
// writes a skill per module, so one a project kept from it is a description
// nothing updates or compares. skills-out-of-date never saw it -- no origin
// hands out a skill of that name -- and this rule is what names the file.
func TestASkillTheGeneratorWroteIsReportedAsRetired(t *testing.T) {
	for _, c := range []struct {
		name, source string
		reported     bool
	}{
		{"written by make:module", "aru@v0.66.0", true},
		{"written by a build from source", "aru@dev", true},
		{"the project's own, with no source", "", false},
		{"from a module", "hyz-is/arandu-tags@v0.4.0", false},
		{"from the skeleton", "arandu-io/arandu@v0.32.0", false},
		{"from a repository that only begins with aru", "aruba-io/tools@v1.0.0", false},
		{"a source with no version", "aru", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := structureProject(t, map[string]string{
				".agents/skills/invoices/SKILL.md": skillWithSource("invoices", c.source),
			})
			got := findingsOf(t, root, "generated-skill-retired")
			if !c.reported {
				if len(got) != 0 {
					t.Fatalf("got %d finding(s), want none: %v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d finding(s), want 1: %v", len(got), got)
			}
			f := got[0]
			if f.File != ".agents/skills/invoices/SKILL.md" || f.Line != 6 {
				t.Errorf("reported at %s:%d, want .agents/skills/invoices/SKILL.md:6, the source line", f.File, f.Line)
			}
			for _, want := range []string{"invoices", c.source, "no longer writes"} {
				if !strings.Contains(f.Message, want) {
					t.Errorf("the message does not say %q: %s", want, f.Message)
				}
			}
			for _, want := range []string{"Delete it", "removing the source and digest lines"} {
				if !strings.Contains(f.Why, want) {
					t.Errorf("the reason does not say %q: %s", want, f.Why)
				}
			}
		})
	}
}
