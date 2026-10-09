package skills_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/skills"
)

// procedure is a skill as an origin publishes it.
const procedure = `---
name: arandu-view
description: Write or change a page. Use when the request mentions a view.
license: MIT
---

# Writing a view

A view is Go. Run aru view:build.
`

func stamped(t *testing.T, content, source string) []byte {
	t.Helper()
	out, err := skills.Stamp([]byte(content), source)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	return out
}

// TestAStampRecordsTheSourceAndTheDigestOfTheRest: the header names the
// origin and the digest of the file as published, and stamping is a function
// of the file -- twice is once, and stamping again with another source
// replaces the first rather than adding to it.
func TestAStampRecordsTheSourceAndTheDigestOfTheRest(t *testing.T) {
	out := stamped(t, procedure, "arandu-io/arandu@v0.29.1")

	header := skills.ParseHeader(out)
	if header.Name != "arandu-view" || header.Source != "arandu-io/arandu@v0.29.1" {
		t.Errorf("header = %+v", header)
	}
	if header.Digest != skills.Digest([]byte(procedure)) {
		t.Errorf("the recorded digest %s is not the digest of the published file, %s", header.Digest, skills.Digest([]byte(procedure)))
	}
	if skills.Digest(out) != header.Digest {
		t.Error("stamping changed the digest of the file it stamped")
	}
	if skills.Edited(out) {
		t.Error("a file fresh from Stamp reads as edited")
	}
	if !strings.HasPrefix(header.Digest, "sha256:") || len(header.Digest) != len("sha256:")+64 {
		t.Errorf("the digest is not sha256: and 64 hex digits: %q", header.Digest)
	}
	if again := stamped(t, string(out), "arandu-io/arandu@v0.29.1"); string(again) != string(out) {
		t.Errorf("stamping twice is not stamping once:\n%s", again)
	}
	other := stamped(t, string(out), "hyz-is/arandu-tags@v0.4.0")
	if strings.Count(string(other), "source:") != 1 || skills.ParseHeader(other).Source != "hyz-is/arandu-tags@v0.4.0" {
		t.Errorf("a second stamp did not replace the first:\n%s", other)
	}
	if !strings.HasSuffix(string(out), "A view is Go. Run aru view:build.\n") {
		t.Errorf("the body moved:\n%s", out)
	}
}

// TestAStampGoesUnderTheMetadataAlreadyThere: a module's skill carries
// audience under metadata, and the stamp joins it rather than opening a
// second metadata key, which a YAML reader would refuse.
func TestAStampGoesUnderTheMetadataAlreadyThere(t *testing.T) {
	source := strings.Replace(procedure, "license: MIT\n", "license: MIT\nmetadata:\n    audience: app\n", 1)
	out := string(stamped(t, source, "hyz-is/arandu-wallet@v0.9.1"))

	if strings.Count(out, "metadata:") != 1 {
		t.Errorf("two metadata keys:\n%s", out)
	}
	if !strings.Contains(out, "metadata:\n    source: hyz-is/arandu-wallet@v0.9.1\n    digest: ") {
		t.Errorf("the stamp does not use the indentation already there:\n%s", out)
	}
	header := skills.ParseHeader([]byte(out))
	if header.Audience != "app" || header.Digest != skills.Digest([]byte(source)) {
		t.Errorf("header = %+v", header)
	}
}

// TestAStampIsRefusedWhereItHasNowhereToGo: a file without frontmatter is not
// a skill, and metadata written inline would have to be parsed to be extended.
func TestAStampIsRefusedWhereItHasNowhereToGo(t *testing.T) {
	if _, err := skills.Stamp([]byte("# no frontmatter\n"), "x/y@v1.0.0"); !errors.Is(err, skills.ErrNoFrontmatter) {
		t.Errorf("no frontmatter: %v", err)
	}
	inline := strings.Replace(procedure, "license: MIT\n", "license: MIT\nmetadata: {}\n", 1)
	if _, err := skills.Stamp([]byte(inline), "x/y@v1.0.0"); !errors.Is(err, skills.ErrInlineMetadata) {
		t.Errorf("inline metadata: %v", err)
	}
}

// TestAnEditInsideTheCustomBlockIsNotAnEdit, and one outside it is.
func TestAnEditInsideTheCustomBlockIsNotAnEdit(t *testing.T) {
	withBlock := procedure + "\n" + skills.BeginCustom + "\n" + skills.EndCustom + "\n"
	out := string(stamped(t, withBlock, "arandu-io/arandu@v0.29.1"))

	inside := strings.Replace(out, skills.BeginCustom+"\n", skills.BeginCustom+"\nOur own rule.\n", 1)
	if skills.Edited([]byte(inside)) {
		t.Error("text written between the custom markers reads as an edit")
	}
	outside := strings.Replace(out, "A view is Go.", "A view is a template.", 1)
	if !skills.Edited([]byte(outside)) {
		t.Error("text changed outside the custom markers does not read as an edit")
	}
	if skills.Edited([]byte(procedure)) {
		t.Error("a file nobody stamped reads as edited")
	}
}

// TestWhereABlockSitsIsNotAnEdit: a block appended to a file whose source has
// none, set apart by blank lines, and a CRLF checkout all keep the digest of
// what was published.
func TestWhereABlockSitsIsNotAnEdit(t *testing.T) {
	want := skills.Digest([]byte(procedure))
	for name, variant := range map[string]string{
		"appended":   procedure + "\n\n" + skills.BeginCustom + "\nOurs.\n" + skills.EndCustom + "\n",
		"in between": strings.Replace(procedure, "\nA view", "\n"+skills.BeginCustom+"\nOurs.\n"+skills.EndCustom+"\n\nA view", 1),
		"crlf":       strings.ReplaceAll(procedure, "\n", "\r\n"),
	} {
		if got := skills.Digest([]byte(variant)); got != want {
			t.Errorf("%s: digest %s, want %s:\n%s", name, got, want, variant)
		}
	}
}

// TestAMarkerQuotedInProseIsNotABlock: a sentence that names the marker is
// about the marker. Only a marker alone on its line opens a block.
func TestAMarkerQuotedInProseIsNotABlock(t *testing.T) {
	prose := procedure + "\nWrite yours after `" + skills.BeginCustom + "` and before\n`" + skills.EndCustom + "`.\n"
	edited := strings.Replace(prose, "after `", "beneath `", 1)
	if skills.Digest([]byte(prose)) == skills.Digest([]byte(edited)) {
		t.Error("prose quoting the markers was read as a custom block and left out of the digest")
	}
}

// TestMergeCarriesTheBlocksByPosition, and appends what has no place in the
// incoming file rather than dropping it.
func TestMergeCarriesTheBlocksByPosition(t *testing.T) {
	block := func(body string) string {
		return skills.BeginCustom + "\n" + body + skills.EndCustom + "\n"
	}
	existing := procedure + "\n" + block("first\n") + "\n" + block("second\n")
	incoming := strings.Replace(procedure, "Run aru view:build.", "Run aru view:build, then aru doctor.", 1) + "\n" + block("default\n")

	merged := string(skills.Merge([]byte(existing), []byte(incoming)))
	if !strings.Contains(merged, "then aru doctor") {
		t.Errorf("the incoming text was not taken:\n%s", merged)
	}
	if strings.Contains(merged, "default") {
		t.Errorf("the incoming block's default replaced the project's first block:\n%s", merged)
	}
	if !strings.Contains(merged, block("first\n")) || !strings.Contains(merged, block("second\n")) {
		t.Errorf("a block was lost:\n%s", merged)
	}
	if strings.Index(merged, "first") > strings.Index(merged, "second") {
		t.Errorf("the blocks changed order:\n%s", merged)
	}
	if got := skills.Merge([]byte(procedure), []byte(incoming)); string(got) != incoming {
		t.Error("a file with no blocks changed what was merged into it")
	}
	if skills.Digest([]byte(merged)) != skills.Digest([]byte(incoming)) {
		t.Error("carrying the blocks changed the digest, so a synced file would read as edited")
	}
}

// TestDiffShowsWhatChanges pins the shape of the preview: the two headers, a
// hunk with its range, and the lines that differ.
func TestDiffShowsWhatChanges(t *testing.T) {
	before := "one\ntwo\nthree\nfour\nfive\n"
	after := "one\ntwo\n3\nfour\nfive\n"
	got := skills.Diff(".agents/skills/x/SKILL.md", []byte(before), []byte(after))
	want := "--- a/.agents/skills/x/SKILL.md\n+++ b/.agents/skills/x/SKILL.md\n@@ -1,5 +1,5 @@\n one\n two\n-three\n+3\n four\n five\n"
	if got != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if d := skills.Diff("p", []byte(before), []byte(before)); d != "" {
		t.Errorf("two equal files diff to %q", d)
	}
	created := skills.Diff("p", nil, []byte("a\nb\n"))
	if !strings.HasPrefix(created, "--- /dev/null\n+++ b/p\n@@ -0,0 +1,2 @@\n+a\n+b\n") {
		t.Errorf("a new file:\n%s", created)
	}
}

// project writes a go.mod and skills under root.
func project(t *testing.T, gomod string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), gomod)
	for path, body := range files {
		write(t, filepath.Join(root, filepath.FromSlash(path)), body)
	}
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheOriginsAreTheSkeletonAndTheRequiredModules: the skeleton at the
// pinned version first, then every module under the owner go.mod requires,
// by path, at the version the build resolves -- and nothing else go.mod names.
func TestTheOriginsAreTheSkeletonAndTheRequiredModules(t *testing.T) {
	root := project(t, `module example.test/app

go 1.26

require (
	github.com/hyz-is/arandu-wallet v0.9.1
	github.com/arandu-io/framework v0.50.2
	github.com/hyz-is/arandu-tags v0.4.0
)

replace github.com/hyz-is/arandu-tags => ./third_party/tags
`, nil)
	origins, err := skills.Origins(root)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, o := range origins {
		labels = append(labels, o.Label())
	}
	want := []string{"arandu-io/arandu@" + skills.SkeletonVersion, "hyz-is/arandu-tags@v0.4.0", "hyz-is/arandu-wallet@v0.9.1"}
	if strings.Join(labels, " ") != strings.Join(want, " ") {
		t.Errorf("origins %v, want %v", labels, want)
	}
	if origins[1].Replaced != filepath.Join(root, "third_party", "tags") {
		t.Errorf("the replaced module's directory is %q", origins[1].Replaced)
	}
	if !origins[2].Names("hyz-is/arandu-wallet@v0.1.0") || origins[2].Names("hyz-is/arandu-wallets@v0.9.1") {
		t.Error("an origin does not recognise its own source at another version, or recognises another's")
	}
}

// TestAnOriginHandsOverOnlyWhatIsForAnApplication: the skeleton hands over its
// arandu- skills and not the example resource's; a module hands over what it
// marks audience: app and not its release procedure.
func TestAnOriginHandsOverOnlyWhatIsForAnApplication(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	skeleton := filepath.Join(cache, "github.com", "arandu-io", "arandu@"+skills.SkeletonVersion)
	write(t, filepath.Join(skeleton, ".agents", "skills", "arandu-view", "SKILL.md"), procedure)
	write(t, filepath.Join(skeleton, ".agents", "skills", "notes", "SKILL.md"), strings.Replace(procedure, "arandu-view", "notes", 1))
	write(t, filepath.Join(skeleton, ".agents", "skills", "README.md"), "# Skills\n")

	dir, ok := skills.Skeleton().Dir()
	if !ok || dir != skeleton {
		t.Fatalf("the skeleton in the cache was not found: %q %v", dir, ok)
	}
	found, err := skills.Skeleton().Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "arandu-view" || found[0].Path() != ".agents/skills/arandu-view/SKILL.md" {
		t.Errorf("the skeleton hands over %+v", found)
	}

	module := filepath.Join(cache, "github.com", "hyz-is", "arandu-wallet@v0.9.1")
	app := strings.Replace(procedure, "license: MIT\n", "license: MIT\nmetadata:\n  audience: app\n", 1)
	write(t, filepath.Join(module, ".agents", "skills", "wallet-module", "SKILL.md"), app)
	write(t, filepath.Join(module, ".agents", "skills", "wallet-release", "SKILL.md"), procedure)
	wallet := skills.Origin{Module: "github.com/hyz-is/arandu-wallet", Version: "v0.9.1"}
	dir, ok = wallet.Dir()
	if !ok {
		t.Fatal("the module in the cache was not found")
	}
	found, err = wallet.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "wallet-module" {
		t.Errorf("the module hands over %+v", found)
	}
}

// TestPlanSaysWhatASyncDoesToEverySkill walks the five outcomes in one
// project, then forces it.
func TestPlanSaysWhatASyncDoesToEverySkill(t *testing.T) {
	origin := skills.Origin{Module: "github.com/hyz-is/arandu-wallet", Version: "v0.9.1"}
	named := func(name, body string) string {
		return strings.Replace(strings.Replace(procedure, "arandu-view", name, 1), "A view is Go. Run aru view:build.", body, 1)
	}
	old := string(stamped(t, named("updated", "Old."), "hyz-is/arandu-wallet@v0.8.0"))
	current := string(stamped(t, named("unchanged", "Same."), origin.Label()))
	edited := strings.Replace(string(stamped(t, named("edited", "Old."), "hyz-is/arandu-wallet@v0.8.0")), "Old.", "Ours.", 1)
	root := project(t, "module example.test/app\n", map[string]string{
		".agents/skills/updated/SKILL.md":   old,
		".agents/skills/unchanged/SKILL.md": current,
		".agents/skills/edited/SKILL.md":    edited,
		".agents/skills/own/SKILL.md":       named("own", "Written here."),
		".agents/skills/foreign/SKILL.md":   string(stamped(t, named("foreign", "Elsewhere."), "hyz-is/arandu-tags@v0.4.0")),
	})
	received := []skills.Received{{Origin: origin, Skills: []skills.Skill{
		{Name: "created", Content: []byte(named("created", "New."))},
		{Name: "updated", Content: []byte(named("updated", "New."))},
		{Name: "unchanged", Content: []byte(named("unchanged", "Same."))},
		{Name: "edited", Content: []byte(named("edited", "New."))},
		{Name: "own", Content: []byte(named("own", "New."))},
		{Name: "foreign", Content: []byte(named("foreign", "New."))},
	}}}

	actions := func(force bool) map[string]skills.Action {
		changes, err := skills.Plan(root, received, force)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]skills.Action{}
		for _, c := range changes {
			out[strings.Split(c.Path, "/")[2]] = c.Action
			if (c.Action == skills.Conflict || c.Action == skills.Kept) && c.Reason == "" {
				t.Errorf("%s is %s with no reason given", c.Path, c.Action)
			}
		}
		return out
	}
	want := map[string]skills.Action{
		"created": skills.Create, "updated": skills.Update, "unchanged": skills.Unchanged,
		"edited": skills.Conflict, "own": skills.Kept, "foreign": skills.Kept,
	}
	got := actions(false)
	for name, action := range want {
		if got[name] != action {
			t.Errorf("%s: %s, want %s", name, got[name], action)
		}
	}
	if forced := actions(true); forced["edited"] != skills.Update || forced["own"] != skills.Kept {
		t.Errorf("forced: edited %s, own %s; want update and kept", forced["edited"], forced["own"])
	}

	changes, err := skills.Plan(root, received, false)
	if err != nil {
		t.Fatal(err)
	}
	written, err := skills.Apply(root, changes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(written, " ") != ".agents/skills/created/SKILL.md .agents/skills/updated/SKILL.md" {
		t.Errorf("applied %v", written)
	}
	again, err := skills.Plan(root, received, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.Action == skills.Create || c.Action == skills.Update {
			t.Errorf("a second sync still has work to do: %s %s", c.Action, c.Path)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "updated", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if skills.ParseHeader(body).Source != origin.Label() || !strings.Contains(string(body), "New.") {
		t.Errorf("the updated skill:\n%s", body)
	}
}

// TestANameTwoOriginsHandOutIsTheFirstOnes: the skeleton is read before the
// modules, and a module that hands out a skill of the same name is reported
// rather than written over it.
func TestANameTwoOriginsHandOutIsTheFirstOnes(t *testing.T) {
	root := project(t, "module example.test/app\n", nil)
	skill := skills.Skill{Name: "arandu-ecosystem", Content: []byte(procedure)}
	changes, err := skills.Plan(root, []skills.Received{
		{Origin: skills.Skeleton(), Skills: []skills.Skill{skill}},
		{Origin: skills.Origin{Module: "github.com/hyz-is/arandu-tags", Version: "v0.4.0"}, Skills: []skills.Skill{skill}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Action != skills.Create || changes[1].Action != skills.Kept ||
		!strings.Contains(changes[1].Reason, skills.Skeleton().Label()) {
		t.Errorf("changes: %+v", changes)
	}
}
