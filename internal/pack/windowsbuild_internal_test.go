package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheWindowsResourceReachesTheProgramAndThenLeaves fixes both halves of one
// defect, and they can only be seen together.
//
// The resource section is handed to the linker by leaving a file in the
// package's own directory: go build links every *_windows_*.syso it finds
// there, without being asked. That makes the file an input with two failure
// modes. Written to the wrong directory it is never linked, and the program is
// built without its icon, manifest and version block -- silently, because
// nothing reports a resource file that was not found. Left behind afterwards it
// is linked into the next build of that package, which is the following
// architecture of the same packaging run and every ordinary build of the
// project after it.
//
// So the program is inspected for the manifest, which proves the file was in
// the directory the toolchain reads, and the directory is inspected afterwards,
// which proves it did not stay there.
func TestTheWindowsResourceReachesTheProgramAndThenLeaves(t *testing.T) {
	pkgDir := probeModule(t)
	out := filepath.Join(t.TempDir(), "probe.exe")
	destination(t, out)

	err := buildWindows(t.TempDir(), &buildInfo{
		appID:   "dev.local.probe",
		archs:   []string{"amd64"},
		minsdk:  10,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe",
		version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4},
	})
	if err != nil {
		t.Fatalf("packaging for Windows: %v", err)
	}

	program, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the program was not written: %v", err)
	}
	if !bytes.Contains(program, []byte(`<assemblyIdentity type="win32" name="probe"`)) {
		t.Error("the program carries no manifest, so the resource file was written somewhere the toolchain does not read")
	}

	if left := sysoIn(t, pkgDir); len(left) > 0 {
		t.Errorf("the resource files %v are still in the package directory, and the next build there would link them", left)
	}
}

// TestTheWindowsResourceLeavesAfterAFailedLink is the same removal on the path
// that skips it.
//
// A build that fails is exactly when a stray file is most expensive: whoever
// fixes the failure builds again in that directory, and the resource section of
// the attempt that failed is linked into the result.
func TestTheWindowsResourceLeavesAfterAFailedLink(t *testing.T) {
	pkgDir := probeModule(t)
	destination(t, filepath.Join(t.TempDir(), "probe.exe"))

	err := buildWindows(t.TempDir(), &buildInfo{
		appID:   "dev.local.probe",
		archs:   []string{"amd64"},
		minsdk:  10,
		name:    "probe",
		pkgDir:  pkgDir,
		pkgPath: "example.test/probe/there-is-no-such-package",
		version: Semver{Major: 1, VersionCode: 1},
	})
	if err == nil {
		t.Fatal("a package that does not exist was packaged")
	}

	if left := sysoIn(t, pkgDir); len(left) > 0 {
		t.Errorf("the resource files %v survived a failed build", left)
	}
}

// probeModule writes the smallest module that can be built for Windows and
// makes it the working directory.
//
// A module of its own, and not a package of this repository: the resource file
// is written into the package's directory, and a test that proves it is removed
// has to be able to say the directory was empty to begin with.
func probeModule(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.test/probe\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The go tool refuses a module that no workspace lists, and the suite may
	// be run from inside one.
	t.Setenv("GOWORK", "off")
	t.Chdir(dir)

	// t.TempDir answers a path under a symlinked directory on macOS, and the go
	// tool reports the resolved one. The directory is read back from the
	// process so the two halves of every assertion below are the same string.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// destination points the packaging sources at one output file for the length of
// a test.
func destination(t *testing.T, path string) {
	t.Helper()

	previous := *destPath
	*destPath = path
	t.Cleanup(func() { *destPath = previous })
}

// sysoIn answers the resource files left in a directory.
func sysoIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".syso") {
			left = append(left, entry.Name())
		}
	}
	return left
}
