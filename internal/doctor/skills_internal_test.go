package doctor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gomod"
	"github.com/arandu-io/aru/internal/skills"
)

// skeletonSkill is a skill of the skeleton as the module cache holds it, with
// body as the text under its heading.
func skeletonSkill(name, body string) string {
	return "---\nname: " + name + "\ndescription: The " + name + " procedure. Use when it applies.\nlicense: MIT\n---\n\n# " + name + "\n\n" + body + "\n"
}

// withSkeletonInTheCache points the module cache at a directory holding the
// skeleton, at the version this build pins, with the given skills.
func withSkeletonInTheCache(t *testing.T, files map[string]string) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	root := filepath.Join(cache, filepath.FromSlash(gomod.EscapePath(skills.SkeletonModule)+"@"+skills.SkeletonVersion))
	for name, body := range files {
		writeFile(t, filepath.Join(root, ".agents", "skills", name, "SKILL.md"), body)
	}
}

// skillProject writes a project with the given skills and nothing else that a
// rule reads.
func skillProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/skills\n\ngo 1.26\n")
	for name, body := range files {
		writeFile(t, filepath.Join(root, ".agents", "skills", name, "SKILL.md"), body)
	}
	return root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stamp(t *testing.T, content, source string) string {
	t.Helper()
	out, err := skills.Stamp([]byte(content), source)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func skillFindings(t *testing.T, root string) []string {
	t.Helper()
	findings, err := Run(root, Conventional)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out []string
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "skills-") {
			out = append(out, f.Rule+" "+f.File)
		}
	}
	sort.Strings(out)
	return out
}

// TestTheSkeletonsSkillsAreComparedWithThePinnedRelease is the skeleton's half
// of the two skill rules, which no fixture can hold: the skeleton is not in a
// project's go.mod, so its source is only ever in the module cache.
//
// The project carries one of the skeleton's skills, stamped from an older
// text, and not another. The example resource's skill is in the cache too and
// is never asked for, because a project deletes it with the resource.
func TestTheSkeletonsSkillsAreComparedWithThePinnedRelease(t *testing.T) {
	withSkeletonInTheCache(t, map[string]string{
		"arandu-view":   skeletonSkill("arandu-view", "Compile the views with aru view:build."),
		"arandu-doctor": skeletonSkill("arandu-doctor", "Run aru doctor."),
		"notes":         skeletonSkill("notes", "The example resource."),
	})
	old := stamp(t, skeletonSkill("arandu-view", "Compile the views."), "arandu-io/arandu@v0.1.0")
	root := skillProject(t, map[string]string{"arandu-view": old})

	got := skillFindings(t, root)
	want := []string{
		"skills-missing .agents/skills/arandu-doctor/SKILL.md",
		"skills-out-of-date .agents/skills/arandu-view/SKILL.md",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// Brought up to date, the same project is silent about it.
	current := stamp(t, skeletonSkill("arandu-view", "Compile the views with aru view:build."), "arandu-io/arandu@"+skills.SkeletonVersion)
	writeFile(t, filepath.Join(root, ".agents", "skills", "arandu-view", "SKILL.md"), current)
	if got := skillFindings(t, root); len(got) != 1 || !strings.HasPrefix(got[0], "skills-missing") {
		t.Errorf("an up-to-date skill is still reported: %v", got)
	}
}

// TestAProjectThatCarriesNoneOfTheSkeletonsSkillsIsNotAskedForThem: with the
// skeleton in the cache, a project whose skills name no source -- one created
// before skills recorded it, or one that removed them -- hears nothing about
// the skeleton. A copy without a source under a skeleton name is reported once
// the project carries a stamped one, which is what the second half checks.
func TestAProjectThatCarriesNoneOfTheSkeletonsSkillsIsNotAskedForThem(t *testing.T) {
	withSkeletonInTheCache(t, map[string]string{
		"arandu-view":   skeletonSkill("arandu-view", "Compile the views with aru view:build."),
		"arandu-policy": skeletonSkill("arandu-policy", "Ask the Policy."),
	})
	copied := skeletonSkill("arandu-policy", "Ask the Policy, an older way.")
	root := skillProject(t, map[string]string{"arandu-policy": copied})
	if got := skillFindings(t, root); len(got) != 0 {
		t.Errorf("a project carrying no stamped skeleton skill is asked for them: %v", got)
	}

	stamped := stamp(t, skeletonSkill("arandu-view", "Compile the views with aru view:build."), "arandu-io/arandu@"+skills.SkeletonVersion)
	writeFile(t, filepath.Join(root, ".agents", "skills", "arandu-view", "SKILL.md"), stamped)
	got := skillFindings(t, root)
	if len(got) != 1 || got[0] != "skills-out-of-date .agents/skills/arandu-policy/SKILL.md" {
		t.Errorf("the copy without a source is not reported once the project carries a stamped skill: %v", got)
	}
}

// TestTheNearMissesInGapsStayQuiet reads the module the gaps fixture requires
// and demands silence about every skill it hands out: one recorded at an older
// version whose text did not change, one edited only inside its custom block,
// one whose header names another origin, the module's release procedure that
// is not for an application, and a skill of the project's own.
//
// The origin is asserted first. A rule that read no origin is silent too, and
// that silence would pass here for the right one.
func TestTheNearMissesInGapsStayQuiet(t *testing.T) {
	state := readSkills("testdata/gaps")
	if len(state.received) != 1 || len(state.received[0].Skills) != 3 {
		t.Fatalf("the gaps fixture's module was not read as one origin handing out three skills: %+v", state.received)
	}
	if got := skillFindings(t, "testdata/gaps"); len(got) != 0 {
		t.Errorf("a near miss was reported:\n  %s", strings.Join(got, "\n  "))
	}
}

// TestTheSkillRulesAreSilentWithoutTheSource: the doctor never downloads, so a
// stamped project on a machine whose cache does not hold the pinned skeleton
// is a project these rules say nothing about.
func TestTheSkillRulesAreSilentWithoutTheSource(t *testing.T) {
	t.Setenv("GOMODCACHE", t.TempDir())
	stamped := stamp(t, skeletonSkill("arandu-view", "Anything."), "arandu-io/arandu@v0.1.0")
	root := skillProject(t, map[string]string{"arandu-view": stamped})
	if got := skillFindings(t, root); len(got) != 0 {
		t.Errorf("findings without the source on disk: %v", got)
	}
}
