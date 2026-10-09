package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/skills"
)

// skillOf generates the module and answers its skill.
func skillOf(t *testing.T, m gen.Module) gen.File {
	t.Helper()
	files, err := gen.Generate(m)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range files {
		if filepath.Base(f.Path) == skills.File {
			return f
		}
	}
	t.Fatal("the module was generated without a skill")
	return gen.File{}
}

// TestTheGeneratedSkillRecordsTheGeneratorAsItsSource: the skill a module
// carries says which aru wrote it, with the digest of what it wrote, the same
// header a skill from the skeleton or a module carries. A sync and the doctor
// read that header to leave the skill to the generator.
func TestTheGeneratedSkillRecordsTheGeneratorAsItsSource(t *testing.T) {
	m := spec(true)
	m.Generator = "v9.9.9"
	skill := skillOf(t, m)

	header := skills.ParseHeader(skill.Content)
	if header.Source != "aru@v9.9.9" {
		t.Errorf("the skill records %q as its source, want aru@v9.9.9", header.Source)
	}
	if header.Digest != skills.Digest(skill.Content) {
		t.Errorf("the recorded digest %q is not the digest of the file, %q", header.Digest, skills.Digest(skill.Content))
	}
	if skills.Edited(skill.Content) {
		t.Error("a skill fresh from the generator reads as edited")
	}

	if got := skills.ParseHeader(skillOf(t, spec(true)).Content).Source; got != "aru@dev" {
		t.Errorf("with no version given the source is %q, want aru@dev, the version an unversioned build reports", got)
	}
}

// TestRegeneratingKeepsTheSkillsCustomBlock: --force rewrites the skill and
// keeps what was written between its custom markers, and drops an edit made
// outside them -- which is what --force says it does.
func TestRegeneratingKeepsTheSkillsCustomBlock(t *testing.T) {
	root := t.TempDir()
	skill := skillOf(t, spec(true))
	if _, _, err := gen.Write(root, []gen.File{skill}, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, skill.Path)
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	const note = "Only the purchasing team approves an order above the limit.\n"
	edited := strings.Replace(string(written), skills.BeginCustom+"\n", skills.BeginCustom+"\n"+note, 1)
	edited = strings.Replace(edited, "# The PurchaseOrder module", "# Purchase orders, as we call them", 1)
	if edited == string(written) || !strings.Contains(edited, note) {
		t.Fatal("the generated skill has no custom block to write into")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := gen.Write(root, []gen.File{skill}, true); err != nil {
		t.Fatal(err)
	}
	regenerated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(regenerated), note) {
		t.Errorf("regenerating dropped the custom block:\n%s", regenerated)
	}
	if strings.Contains(string(regenerated), "as we call them") {
		t.Errorf("regenerating kept an edit made outside the custom block:\n%s", regenerated)
	}
	if skills.Edited(regenerated) {
		t.Error("a regenerated skill with text only in its custom block reads as edited")
	}
}

// TestTheGeneratedSkillSaysWhatTheCodeDoes keeps out the sentences the skill
// used to carry and the code never did: a module from make:module keeps no
// specification to regenerate from, a Policy does not issue the Grant, the
// Service is not the only caller of the Model, a field is more than three
// lines, and the gates are eight commands rather than one chained line.
func TestTheGeneratedSkillSaysWhatTheCodeDoes(t *testing.T) {
	for _, tenant := range []bool{true, false} {
		body := string(skillOf(t, spec(tenant)).Content)
		for _, stale := range []string{
			"Generated from a specification",
			"the only thing that issues a Grant",
			"the only consumer of the Model entry point",
			"one in the model and one in the service's `fill`.",
			"aru view:build && go build",
		} {
			if strings.Contains(body, stale) {
				t.Errorf("tenant=%v: the skill still says %q", tenant, stale)
			}
		}
		for _, want := range []string{
			"aru model:build\naru view:build\ngofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')\ngo build ./...\ngo vet ./...\ngo test -race ./...\naru doctor\n",
			"`database/specs/purchase_order.yaml`",
			"a new migration that adds\n  the column",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("tenant=%v: the skill does not say %q", tenant, want)
			}
		}
	}
}
