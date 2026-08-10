package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kyseCheckout is the sibling component library, used through a replace so the
// test resolves a directory without reaching the network.
func kyseCheckout(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(wd), "kyse")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Skipf("no checkout at %s: this test needs the sibling repository", dir)
	}
	return dir
}

// outsideTheWorkspace makes the temporary project the whole build list.
//
// A developer of this repository has GOWORK pointing at the workspace that
// contains kyse, and the go command honours it from any directory -- so every
// test below resolved the real checkout no matter what its own go.mod said, and
// the one that asserts a library cannot be found would have found it. CI has no
// workspace, which is where that difference would have surfaced.
func outsideTheWorkspace(t *testing.T) {
	t.Helper()
	t.Setenv("GOWORK", "off")
}

// TestTailwindIsToldWhereTheImportedComponentsLive is the unit half of a defect
// that only showed up as a page.
//
// The components are an imported module (ADR 0027) and the project's stylesheet
// can only declare directories of the project, so Tailwind never read a single
// class name out of kyse. `.text-destructive`, which components.Field writes on
// the error line of every form, was absent from 193 KB of compiled CSS: a
// rejected form explained itself in the body colour, and no build said so.
func TestTailwindIsToldWhereTheImportedComponentsLive(t *testing.T) {
	goTool(t)
	outsideTheWorkspace(t)
	library := kyseCheckout(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/shop\n\ngo 1.25.0\n\nrequire github.com/arandu-io/kyse v0.0.0\n\nreplace github.com/arandu-io/kyse => "+filepath.ToSlash(library)+"\n")

	input, err := stylesheetInput(root)
	if err != nil {
		t.Fatal(err)
	}

	want := "@source \"" + filepath.ToSlash(library) + "/**/*.go\";"
	if !strings.Contains(input, want) {
		t.Errorf("Tailwind is not told where the imported components are, so every class only they render is missing from the page.\n  expected  %s\n  compiled\n%s", want, input)
	}
	if !strings.Contains(input, filepath.ToSlash(filepath.Join(root, stylesheetSource))) {
		t.Errorf("the project's own stylesheet is not in what Tailwind compiles, which is the whole page:\n%s", input)
	}
}

// TestAProjectThatImportsNoComponentsStillCompiles keeps the fix from becoming a
// requirement.
//
// Depending on the component library is a choice: a project can write every
// class in its own views. Resolving a module that is not in the build list has
// to be an empty answer, not a failed build -- otherwise `aru view:build` starts
// refusing projects that were fine before this existed.
func TestAProjectThatImportsNoComponentsStillCompiles(t *testing.T) {
	goTool(t)
	outsideTheWorkspace(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/plain\n\ngo 1.25.0\n")

	input, err := stylesheetInput(root)
	if err != nil {
		t.Fatalf("a project that imports no components cannot build its stylesheet: %v", err)
	}
	if strings.Contains(input, "@source") {
		t.Errorf("a source was declared for a component library this project does not depend on:\n%s", input)
	}
}

// TestAVendoredComponentLibraryIsStillFound covers the copy of the source that
// is not in the module cache.
//
// `go mod vendor` puts the dependencies inside the project, and from then on
// `go list -m` reports no directory for any module at all -- it exits 0 with an
// empty line, because in vendor mode there is no cache to point at. Reading that
// as "this project does not use the component library" put a vendored project
// straight back into the defect this file was written to close: same views, same
// components, a stylesheet 2.5 KB smaller with none of their classes in it, and
// a green build. Measured on a project whose only difference was a vendor
// directory: `.size-3` and `.rounded-full` present without it, absent with it.
func TestAVendoredComponentLibraryIsStillFound(t *testing.T) {
	goTool(t)
	outsideTheWorkspace(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/vendored\n\ngo 1.25.0\n\nrequire github.com/arandu-io/kyse v0.2.0\n")
	writeFile(t, filepath.Join(root, "vendor", "modules.txt"), "# github.com/arandu-io/kyse v0.2.0\n## explicit; go 1.25\ngithub.com/arandu-io/kyse/components\n")
	vendored := filepath.Join(root, "vendor", "github.com", "arandu-io", "kyse", "components")
	writeFile(t, filepath.Join(vendored, "button.go"), "package components\n")

	input, err := stylesheetInput(root)
	if err != nil {
		t.Fatalf("a project that vendors its dependencies cannot build its stylesheet: %v", err)
	}

	want := "@source \"" + filepath.ToSlash(filepath.Dir(vendored)) + "/**/*.go\";"
	if !strings.Contains(input, want) {
		t.Errorf("the vendored copy of the components is not what Tailwind reads, so vendoring silently removes every class only they render.\n  expected  %s\n  compiled\n%s", want, input)
	}
}

// TestAComponentLibraryThatCannotBeFoundStopsTheBuild is the rule that keeps the
// fix from having a quiet way out.
//
// A project that requires the components and cannot produce their source is not
// a project that writes its own markup: it is a build about to ship pages
// missing the classes eleven components render. Nothing downstream notices --
// Tailwind does not report a source that matched no file, the browser does not
// report a class with no rule, and the page renders. So the only place this can
// be said is here, at the moment the directory comes back empty.
//
// The situation is ordinary rather than exotic: a machine with no network and a
// module cache that has never seen the library, which is a build agent on its
// first run.
func TestAComponentLibraryThatCannotBeFoundStopsTheBuild(t *testing.T) {
	goTool(t)
	outsideTheWorkspace(t)
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/offline\n\ngo 1.25.0\n\nrequire github.com/arandu-io/kyse v0.2.0\n")

	input, err := stylesheetInput(root)
	if err == nil {
		t.Fatalf("a stylesheet was compiled without the components the project requires, and nothing said so:\n%s", input)
	}
	if !strings.Contains(err.Error(), componentLibrary) {
		t.Errorf("the build stopped without naming the library it could not find, which is the one thing the person has to know to fix it: %v", err)
	}
}
