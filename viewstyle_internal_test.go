package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStyleProject lays out the two directories a block is read from and
// returns the project root.
func writeStyleProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestAScopedBlockIsCompiledUnderItsOwnClass is the whole feature in one
// assertion: what the caller wrote comes out as rules, and & is the class the
// render will ask for.
func TestAScopedBlockIsCompiledUnderItsOwnClass(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"storage/framework/views/home.go": `package views

import "github.com/arandu-io/kyse"

var _ = kyse.CSS("& { gap: 6px; }\n& [data-part=\"title\"] { letter-spacing: -0.01em; }")
`,
	})

	got, err := scopedStylesheet(root)
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}

	class := styleClass("& { gap: 6px; }\n& [data-part=\"title\"] { letter-spacing: -0.01em; }")
	for _, want := range []string{
		"." + class + " { gap: 6px; }",
		"." + class + ` [data-part="title"] { letter-spacing: -0.01em; }`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the stylesheet does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "&") {
		t.Errorf("an & survived into the stylesheet:\n%s", got)
	}
}

// TestTheClassMatchesTheOneTheRenderWillAsk is the agreement the two sides
// depend on, and there is no table between them to check it against -- so it is
// checked here, against the shape both compute.
//
// The framework's view.StyleClass is the other half. It cannot be imported from
// this module, which is exactly why both have a name and a test.
func TestTheClassMatchesTheOneTheRenderWillAsk(t *testing.T) {
	const block = "& { gap: 6px; }"

	got := styleClass(block)
	if len(got) != len("k-")+12 || !strings.HasPrefix(got, "k-") {
		t.Fatalf("styleClass(%q) = %q, want k- and twelve hex characters", block, got)
	}
	if again := styleClass(block); again != got {
		t.Fatalf("styleClass is not stable: %q then %q", got, again)
	}
	if other := styleClass("& { gap: 8px; }"); other == got {
		t.Fatalf("two different blocks share the class %q", got)
	}
	// The hash is of the bytes as they were written. Nothing trims, because a
	// normalisation is a second thing to implement identically on the other
	// side of a module boundary.
	if styleClass("  "+block) == got {
		t.Fatal("styleClass folded whitespace, which puts a second implementation of the folding in the framework")
	}
}

// TestABlockBuiltAtRunTimeIsRefused is the failure this pass exists to make
// loud. The render can hash anything; only what is written here becomes a rule.
func TestABlockBuiltAtRunTimeIsRefused(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/Catalog/entries.go": `package catalog

import "github.com/arandu-io/kyse"

func block(gap string) kyse.CSS { return kyse.CSS("& { gap: " + gap + "; }") }
`,
	})

	_, err := scopedStylesheet(root)
	if err == nil {
		t.Fatal("a block assembled from a variable was accepted")
	}
	for _, want := range []string{"entries.go", "written out here"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}

// TestABlockWithNoAmpersandIsRefused: & is what makes the rules the component's.
// Without one they are rules for the whole page, written under a name that says
// they are not.
func TestABlockWithNoAmpersandIsRefused(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/x.go": `package app

import "github.com/arandu-io/kyse"

var _ = kyse.CSS("p { color: red; }")
`,
	})

	_, err := scopedStylesheet(root)
	if err == nil {
		t.Fatal("a block with no & was accepted")
	}
	if !strings.Contains(err.Error(), "no & that is a selector") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// TestALiteralSplitAcrossLinesIsStillALiteral. A block long enough to be worth
// scoping is a block somebody wraps, and refusing a + between two literals would
// be refusing formatting.
func TestALiteralSplitAcrossLinesIsStillALiteral(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/x.go": `package app

import "github.com/arandu-io/kyse"

var _ = kyse.CSS("& { gap: 6px; }" +
	"& > p { margin: 0; }")
`,
	})

	got, err := scopedStylesheet(root)
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}
	if !strings.Contains(got, "> p { margin: 0; }") {
		t.Errorf("the second half of the block was dropped:\n%s", got)
	}
}

// TestAnAmpersandInsideQuotesIsNotASelector is the one case a substitution gets
// wrong if it does not look at quotes, and it is worth the eight lines that
// avoid it: content: "&" is an ampersand on the page.
func TestAnAmpersandInsideQuotesIsNotASelector(t *testing.T) {
	got, replaced := scopeRules(`&::after { content: "&"; }`, "k-abc123abc123")

	if want := `.k-abc123abc123::after`; !strings.Contains(got, want) {
		t.Errorf("the selector was not scoped: %s", got)
	}
	if want := `content: "&";`; !strings.Contains(got, want) {
		t.Errorf("the ampersand inside the quotes was replaced: %s", got)
	}
	if replaced != 1 {
		t.Errorf("scopeRules replaced %d ampersands, want 1", replaced)
	}
}

// TestAnApostropheInACommentDoesNotSwallowTheBlock is the defect the quote
// scanner shipped with.
//
// It read quotes and knew nothing of comments, so the apostrophe in ordinary
// English prose opened quote mode and nothing ever closed it: every & below the
// comment was left alone, the element still got its class, and the stylesheet
// got a bare & at the top of a rule that then applied to nothing.
func TestAnApostropheInACommentDoesNotSwallowTheBlock(t *testing.T) {
	got, replaced := scopeRules("/* the card's own gap */\n& { gap: 6px; }", "k-abc123abc123")

	if replaced != 1 {
		t.Fatalf("scopeRules replaced %d ampersands, want 1:\n%s", replaced, got)
	}
	if strings.Contains(got, "& {") {
		t.Errorf("the selector after the comment was left unscoped:\n%s", got)
	}
}

// TestAnAmpersandInAQueryStringIsNotASelector: the & of url(x?a=1&b=2) separates
// two parameters, and replacing it writes an address nothing serves.
func TestAnAmpersandInAQueryStringIsNotASelector(t *testing.T) {
	got, _ := scopeRules("& { background-image: url(/i/x.png?a=1&b=2); }", "k-abc123abc123")

	if !strings.Contains(got, "url(/i/x.png?a=1&b=2)") {
		t.Errorf("the query string was rewritten:\n%s", got)
	}
	if !strings.Contains(got, ".k-abc123abc123 {") {
		t.Errorf("the selector was not scoped:\n%s", got)
	}
}

// TestABlockWhoseOnlyAmpersandIsNotASelectorIsRefused closes the gap between the
// guard and the substitution. The guard used to ask whether the text contained
// an &; this asks whether the substitution found one, which is the same question
// the output answers.
func TestABlockWhoseOnlyAmpersandIsNotASelectorIsRefused(t *testing.T) {
	for _, block := range []string{
		`a::after { content: "&"; }`,
		"/* a & b */\np { color: red; }",
		"p { background-image: url(/i/x.png?a=1&b=2); }",
	} {
		root := writeStyleProject(t, map[string]string{
			"app/x.go": "package app\n\nimport \"github.com/arandu-io/kyse\"\n\nvar _ = kyse.CSS(`" + block + "`)\n",
		})
		if _, err := scopedStylesheet(root); err == nil {
			t.Errorf("a block whose only & is not a selector was accepted: %s", block)
		}
	}
}

// TestADotImportIsRefusedRatherThanLost. A dot import writes CSS(…) with no
// package in front, which this pass cannot tell from any other call of that
// name -- so the block would compile to nothing and the element would carry a
// class no rule was emitted for.
func TestADotImportIsRefusedRatherThanLost(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/x.go": `package app

import . "github.com/arandu-io/kyse"

var _ = CSS("& { gap: 6px; }")
`,
	})

	_, err := scopedStylesheet(root)
	if err == nil {
		t.Fatal("a dot import was accepted, and the block in it would be lost")
	}
	if !strings.Contains(err.Error(), "imported with a dot") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// TestTheSameBlockIsCompiledOnce. Two callers writing the same block get the
// same class, so emitting both would put the same declarations in the
// stylesheet twice.
func TestTheSameBlockIsCompiledOnce(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/a.go": `package app

import "github.com/arandu-io/kyse"

var A = kyse.CSS("& { gap: 6px; }")
`,
		"app/b.go": `package app

import "github.com/arandu-io/kyse"

var B = kyse.CSS("& { gap: 6px; }")
`,
	})

	got, err := scopedStylesheet(root)
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}
	if n := strings.Count(got, "gap: 6px"); n != 1 {
		t.Errorf("the block was compiled %d times, want 1:\n%s", n, got)
	}
}

// TestAnAliasedImportIsStillTheComponentModule. An alias is legal Go, and a
// pass that only knew one spelling would emit no rule for the file that used
// the other -- silently, which is the failure mode of this whole feature.
func TestAnAliasedImportIsStillTheComponentModule(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/x.go": `package app

import k "github.com/arandu-io/kyse"

var _ = k.CSS("& { gap: 6px; }")
`,
	})

	got, err := scopedStylesheet(root)
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}
	if !strings.Contains(got, "gap: 6px") {
		t.Errorf("the aliased call was not read:\n%s", got)
	}
}

// TestACSSCallFromAnotherPackageIsNotOurs, because "CSS" is a name anybody may
// use and reading one would put somebody else's string into the stylesheet.
func TestACSSCallFromAnotherPackageIsNotOurs(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"app/x.go": `package app

import "example.com/other/kyse"

var _ = kyse.CSS("& { gap: 6px; }")
`,
	})

	got, err := scopedStylesheet(root)
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}
	if got != "" {
		t.Errorf("a call from another module was compiled:\n%s", got)
	}
}

// TestAProjectWithNoBlocksAddsNothing. Most projects write none, and a stray
// comment or a blank line appended to every stylesheet is a diff in every build.
func TestAProjectWithNoBlocksAddsNothing(t *testing.T) {
	got, err := scopedStylesheet(writeStyleProject(t, map[string]string{
		"app/x.go": "package app\n",
	}))
	if err != nil {
		t.Fatalf("scopedStylesheet: %v", err)
	}
	if got != "" {
		t.Errorf("a project with no blocks got %q", got)
	}
}

// TestAViewThatDoesNotParseIsNotThisPassProblem. The view build has already
// refused what it could not compile, and the project's own Go is the compiler's
// to complain about -- in a message about the mistake rather than about a
// stylesheet.
func TestAViewThatDoesNotParseIsNotThisPassProblem(t *testing.T) {
	got, err := scopedStylesheet(writeStyleProject(t, map[string]string{
		"app/x.go": "package app\nfunc broken( {\n",
	}))
	if err != nil {
		t.Fatalf("a file that does not parse was reported as a stylesheet problem: %v", err)
	}
	if got != "" {
		t.Errorf("got %q", got)
	}
}

// TestTheRefusalNamesTheViewAndNotTheGeneratedGo. A person writes a .kyse.go and
// has never opened what the build wrote from it; a line number in the second is
// a line number in a file they cannot fix.
func TestTheRefusalNamesTheViewAndNotTheGeneratedGo(t *testing.T) {
	root := writeStyleProject(t, map[string]string{
		"storage/framework/views/home.go": `package views

import "github.com/arandu-io/kyse"

//line resources/views/home.kyse.go:42
var _ = kyse.CSS(gap)
`,
	})

	_, err := scopedStylesheet(root)
	if err == nil {
		t.Fatal("a block built from a name was accepted")
	}
	if !strings.Contains(err.Error(), "resources/views/home.kyse.go:42") {
		t.Errorf("the refusal points at the generated file rather than the view: %v", err)
	}
}
