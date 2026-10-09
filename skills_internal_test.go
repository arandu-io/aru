package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gomod"
	"github.com/arandu-io/aru/internal/skills"
)

// skeletonProcedure is a skill as the skeleton publishes it.
func skeletonProcedure(name, body string) string {
	return "---\nname: " + name + "\ndescription: The " + name + " procedure. Use when it applies.\nlicense: MIT\n---\n\n# " + name + "\n\n" + body + "\n"
}

// syncProject writes a project and points the module cache at a directory
// holding the skeleton, at the pinned version, with the given skills. The
// network is switched off, so a run that tried to download would fail rather
// than reach it.
func syncProject(t *testing.T, skeleton map[string]string) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")
	dir := filepath.Join(cache, filepath.FromSlash(gomod.EscapePath(skills.SkeletonModule)+"@"+skills.SkeletonVersion))
	for name, body := range skeleton {
		writeSkill(t, dir, name, body)
	}

	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example.test/app\n\ngo 1.26\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"arandu.toml": "name = \"app\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, ".agents", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSkill(t *testing.T, root, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".agents", "skills", name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestSkillsSyncShowsBeforeItWrites: without --apply nothing is written and
// the preview carries the line and the diff of every skill; with it the skill
// lands stamped; and a second run has nothing left to do. The example
// resource's skill is never offered.
func TestSkillsSyncShowsBeforeItWrites(t *testing.T) {
	root := syncProject(t, map[string]string{
		"arandu-view": skeletonProcedure("arandu-view", "Run aru view:build."),
		"notes":       skeletonProcedure("notes", "The example resource."),
	})

	code, stdout, stderr := exercise(t, "skills:sync")
	if code != 0 {
		t.Fatalf("skills:sync exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"arandu-io/arandu@" + skills.SkeletonVersion + ": 1 skill(s) for an application",
		"create  .agents/skills/arandu-view/SKILL.md",
		"+++ b/.agents/skills/arandu-view/SKILL.md",
		"+  source: arandu-io/arandu@" + skills.SkeletonVersion,
		"nothing was written; aru skills:sync --apply writes the 1 file(s)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the preview does not say %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "notes") {
		t.Errorf("the example resource's skill was offered:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("the preview wrote something: %v", err)
	}

	if code, _, stderr := exercise(t, "skills:sync", "--apply"); code != 0 {
		t.Fatalf("skills:sync --apply exited %d: %s", code, stderr)
	}
	header := skills.ParseHeader([]byte(readSkill(t, root, "arandu-view")))
	if header.Source != "arandu-io/arandu@"+skills.SkeletonVersion || header.Digest == "" {
		t.Errorf("the written skill's header: %+v", header)
	}

	_, stdout, _ = exercise(t, "skills:sync")
	if !strings.Contains(stdout, "unchanged  .agents/skills/arandu-view/SKILL.md") || strings.Contains(stdout, "nothing was written") {
		t.Errorf("a second run still has work to do:\n%s", stdout)
	}
}

// TestSkillsSyncLeavesAnEditAloneUntilForced: a stamped skill edited outside
// its custom block is a conflict -- shown, not written, and an --apply that
// leaves one says so in its exit status. --force replaces the edit and keeps
// the block.
func TestSkillsSyncLeavesAnEditAloneUntilForced(t *testing.T) {
	root := syncProject(t, map[string]string{
		"arandu-view": skeletonProcedure("arandu-view", "Run aru view:build, then aru doctor."),
	})
	old, err := skills.Stamp([]byte(skeletonProcedure("arandu-view", "Run aru view:build.")), "arandu-io/arandu@v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(old), "# arandu-view", "# Our views", 1) +
		"\n" + skills.BeginCustom + "\nOur screens use the wide layout.\n" + skills.EndCustom + "\n"
	writeSkill(t, root, "arandu-view", edited)

	_, stdout, _ := exercise(t, "skills:sync")
	if !strings.Contains(stdout, "conflict  .agents/skills/arandu-view/SKILL.md") || !strings.Contains(stdout, "edited outside its custom block") {
		t.Errorf("the edit is not reported as a conflict:\n%s", stdout)
	}

	code, _, stderr := exercise(t, "skills:sync", "--apply")
	if code == 0 || !strings.Contains(stderr, "1 skill(s) edited outside their custom block were left as they are") {
		t.Errorf("--apply over a conflict exited %d: %s", code, stderr)
	}
	if readSkill(t, root, "arandu-view") != edited {
		t.Fatal("--apply wrote over an edit it was not told to replace")
	}

	if code, _, stderr := exercise(t, "skills:sync", "--apply", "--force"); code != 0 {
		t.Fatalf("--apply --force exited %d: %s", code, stderr)
	}
	forced := readSkill(t, root, "arandu-view")
	if strings.Contains(forced, "# Our views") || !strings.Contains(forced, "then aru doctor") {
		t.Errorf("--force did not replace the edit:\n%s", forced)
	}
	if !strings.Contains(forced, "Our screens use the wide layout.") {
		t.Errorf("--force dropped the custom block:\n%s", forced)
	}
	if skills.Edited([]byte(forced)) {
		t.Error("the forced skill reads as edited")
	}
}

// TestSkillsSyncNeverTouchesAProjectsOwnSkill: a skill with no source is the
// project's, under whatever name, and neither --apply nor --force writes it.
func TestSkillsSyncNeverTouchesAProjectsOwnSkill(t *testing.T) {
	root := syncProject(t, map[string]string{
		"arandu-view": skeletonProcedure("arandu-view", "Run aru view:build."),
	})
	own := skeletonProcedure("arandu-view", "Our own way of writing views.")
	writeSkill(t, root, "arandu-view", own)

	code, stdout, stderr := exercise(t, "skills:sync", "--apply", "--force")
	if code != 0 {
		t.Fatalf("exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "kept  .agents/skills/arandu-view/SKILL.md") || !strings.Contains(stdout, "no source in its header") {
		t.Errorf("the project's own skill is not reported as kept:\n%s", stdout)
	}
	if readSkill(t, root, "arandu-view") != own {
		t.Error("a skill with no source was written")
	}
}

// TestSkillsSyncDownloadsWhatIsNotOnDisk: an origin whose version is not in
// the module cache is asked of the toolchain, and with the network off the
// refusal names the download that was attempted rather than passing for a
// skeleton with no skills.
func TestSkillsSyncDownloadsWhatIsNotOnDisk(t *testing.T) {
	syncProject(t, nil)
	t.Setenv("GOMODCACHE", t.TempDir())

	code, _, stderr := exercise(t, "skills:sync")
	if code == 0 {
		t.Fatal("skills:sync succeeded with the skeleton nowhere on disk and the network off")
	}
	if !strings.Contains(stderr, "go mod download "+skills.SkeletonModule+"@"+skills.SkeletonVersion) {
		t.Errorf("the error does not name the download it attempted: %s", stderr)
	}
}

// TestSkillsSyncNeedsAProject: outside one there is no go.mod to read origins
// from and no skills directory to write to.
func TestSkillsSyncNeedsAProject(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := exercise(t, "skills:sync"); code == 0 {
		t.Error("skills:sync succeeded outside a project")
	}
	if code, _, stderr := exercise(t, "skills:sync", "extra"); code == 0 || !strings.Contains(stderr, "not an argument") {
		t.Errorf("a stray argument exited %d: %s", code, stderr)
	}
}

// TestANewProjectsSkillsRecordWhereTheyCameFrom: what `aru new` copies from the
// skeleton is stamped with the pinned release, with the digest of the bytes it
// copied, so the first sync finds nothing to change. The example resource's
// skill is the project's from the start and is left without a source.
func TestANewProjectsSkillsRecordWhereTheyCameFrom(t *testing.T) {
	dir := t.TempDir()
	view := skeletonProcedure("arandu-view", "Run aru view:build.")
	notes := skeletonProcedure("notes", "The example resource.")
	writeSkill(t, dir, "arandu-view", view)
	writeSkill(t, dir, "notes", notes)

	if err := stampSkeletonSkills(dir); err != nil {
		t.Fatal(err)
	}
	stamped := readSkill(t, dir, "arandu-view")
	header := skills.ParseHeader([]byte(stamped))
	if header.Source != "arandu-io/arandu@"+skills.SkeletonVersion || header.Digest != skills.Digest([]byte(view)) {
		t.Errorf("the copied skill's header: %+v", header)
	}
	if readSkill(t, dir, "notes") != notes {
		t.Error("the example resource's skill was stamped as the skeleton's")
	}

	if err := stampSkeletonSkills(t.TempDir()); err != nil {
		t.Errorf("a tree without skills: %v", err)
	}
}

// TestNewStampsTheSkillsItCopies runs `aru new` against a git that writes a
// skeleton with two skills, and reads what landed: the stamping above is only
// worth something if the command calls it.
//
// The git is a shell script, so the test runs where a shell does.
func TestNewStampsTheSkillsItCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for git is a shell script")
	}
	root := t.TempDir()
	t.Chdir(root)
	view := skeletonProcedure("arandu-view", "Run aru view:build.")
	notes := skeletonProcedure("notes", "The example resource.")

	bin := filepath.Join(root, "bin")
	script := "#!/bin/sh\nfor last; do :; done\n" +
		"mkdir -p \"$last/.agents/skills/arandu-view\" \"$last/.agents/skills/notes\"\n" +
		"printf 'APP_KEY=\\n' > \"$last/.env.example\"\n" +
		"cat > \"$last/.agents/skills/arandu-view/SKILL.md\" <<'SKILL'\n" + view + "SKILL\n" +
		"cat > \"$last/.agents/skills/notes/SKILL.md\" <<'SKILL'\n" + notes + "SKILL\n"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := newProject([]string{"my-app"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("aru new: %v", err)
	}
	header := skills.ParseHeader([]byte(readSkill(t, "my-app", "arandu-view")))
	if header.Source != "arandu-io/arandu@"+skills.SkeletonVersion || header.Digest != skills.Digest([]byte(view)) {
		t.Errorf("aru new left the skeleton's skill without its source: %+v", header)
	}
	if readSkill(t, "my-app", "notes") != notes {
		t.Error("aru new stamped the example resource's skill")
	}
}
