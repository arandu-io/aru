package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestTheRuntimeIsFoundByWhatSitsBesideIt fixes the reason the package is
// looked for rather than named.
//
// A path written into these sources is checked by nothing: it is a string
// handed to the linker and a directory handed to a glob, so the day the package
// moves, the build still succeeds. The linker sets a symbol that is not there
// and says nothing about it, and the artifact ships with an empty application
// identifier.
func TestTheRuntimeIsFoundByWhatSitsBesideIt(t *testing.T) {
	runtime := packageWith(t, "example.test/engine/app", "Window.java")

	found, err := findRuntimePackage(graphOf(
		packageWith(t, "example.test/app/cmd/native"),
		runtime,
		packageWith(t, "example.test/app/internal/store"),
	))
	if err != nil {
		t.Fatalf("the runtime was not found: %v", err)
	}
	if found.path != runtime.PkgPath {
		t.Errorf("the runtime is %s, and %s was found", runtime.PkgPath, found.path)
	}
	if want := filepath.Dir(runtime.GoFiles[0]); found.dir != want {
		t.Errorf("the runtime's sources are in %s, and %s was answered", want, found.dir)
	}
}

// TestTheIOSHeaderIsAlsoAMarker keeps the rule working for a target whose
// binding is not Java.
//
// Neither marker is a Go file, which is what lets one rule serve every
// platform: a Go file is in the package or not depending on which target the
// graph was read for, and these two sit in the directory either way.
func TestTheIOSHeaderIsAlsoAMarker(t *testing.T) {
	runtime := packageWith(t, "example.test/engine/app", "framework_ios.h")

	found, err := findRuntimePackage(graphOf(
		packageWith(t, "example.test/app/cmd/native"),
		runtime,
	))
	if err != nil {
		t.Fatalf("the runtime was not found: %v", err)
	}
	if found.path != runtime.PkgPath {
		t.Errorf("the runtime is %s, and %s was found", runtime.PkgPath, found.path)
	}
}

// TestTwoRuntimesAreRefusedByName keeps a guess out of the build.
//
// One module of somebody else's that ships a jar and the Java beside it makes
// the marker match twice, and nothing here knows which of the two a build
// meant. Choosing would choose it in every build afterwards, invisibly, so the
// refusal names both and the person who added the second one decides.
func TestTwoRuntimesAreRefusedByName(t *testing.T) {
	_, err := findRuntimePackage(graphOf(
		packageWith(t, "example.test/app/cmd/native"),
		packageWith(t, "example.test/engine/app", "Window.java"),
		packageWith(t, "example.test/community/widgets", "Widget.java"),
	))
	if err == nil {
		t.Fatal("a graph with two runtimes was accepted")
	}
	for _, named := range []string{"example.test/engine/app", "example.test/community/widgets"} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("the refusal does not name %s: %v", named, err)
		}
	}
}

// TestAGraphWithNoRuntimeIsRefused stops a build that cannot open a window
// before any toolchain is started.
func TestAGraphWithNoRuntimeIsRefused(t *testing.T) {
	_, err := findRuntimePackage(graphOf(
		packageWith(t, "example.test/app/cmd/native"),
		packageWith(t, "example.test/app/internal/store"),
	))
	if err == nil {
		t.Fatal("a graph with no runtime was accepted")
	}
	if !strings.Contains(err.Error(), "example.test/app/cmd/native") {
		t.Errorf("the refusal does not say what was looked through: %v", err)
	}
}

// TestAPackageBehindAConstraintIsStillPlaced answers for the one package the
// graph names without naming a file of it.
//
// A package whose Go sources are all excluded by the target's build constraints
// has no Go file to take a directory from, and it still has a directory: the
// module says where, and the package path says how far below.
func TestAPackageBehindAConstraintIsStillPlaced(t *testing.T) {
	dir := t.TempDir()
	constrained := &packages.Package{
		ID:      "example.test/engine/app",
		PkgPath: "example.test/engine/app",
		Module:  &packages.Module{Path: "example.test/engine", Dir: dir},
	}

	want := filepath.Join(dir, "app")
	if got := packageDir(constrained); got != want {
		t.Errorf("the package is in %s, and %s was answered", want, got)
	}
}

// packageWith answers a package of the graph, with the named files written
// beside its one Go source.
func packageWith(t *testing.T, pkgPath string, beside ...string) *packages.Package {
	t.Helper()

	dir := t.TempDir()
	for _, name := range append([]string{"doc.go"}, beside...) {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &packages.Package{
		ID:      pkgPath,
		PkgPath: pkgPath,
		GoFiles: []string{filepath.Join(dir, "doc.go")},
	}
}

// graphOf makes the first package import all the others.
func graphOf(root *packages.Package, imported ...*packages.Package) *packages.Package {
	root.Imports = make(map[string]*packages.Package, len(imported))
	for _, p := range imported {
		root.Imports[p.PkgPath] = p
	}
	return root
}
