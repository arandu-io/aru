package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheNativeLoopWatchesTheTargetAndNotTheWholeProject is the difference
// between this loop and the web one.
//
// A native application draws for a server, so a handler that changed is
// answered differently on the next request with nothing rebuilt. Rebuilding
// the application for it would mean closing somebody's window, every time they
// saved a file the window does not contain.
func TestTheNativeLoopWatchesTheTargetAndNotTheWholeProject(t *testing.T) {
	root := nativeProject(t, true)
	writeFile(t, filepath.Join(root, "app", "Http", "Controllers", "HomeController.go"), "package controllers\n")
	writeFile(t, filepath.Join(root, nativeDir, "sign_in.go"), "package main\n")

	watched := nativeSnapshot(root)

	screen := filepath.Join(root, nativeDir, "sign_in.go")
	if _, found := watched[screen]; !found {
		t.Error("a screen of the native target is not watched, so editing it rebuilds nothing")
	}

	handler := filepath.Join(root, "app", "Http", "Controllers", "HomeController.go")
	if _, found := watched[handler]; found {
		t.Error("a server handler is watched, so saving one closes the window for a change it does not contain")
	}
}

// TestTheModuleFilesAreWatched keeps a dependency that moved from being
// invisible.
//
// No screen was edited, and every screen is drawn with something different.
func TestTheModuleFilesAreWatched(t *testing.T) {
	root := nativeProject(t, true)
	watched := nativeSnapshot(root)

	for _, name := range []string{"go.mod"} {
		if _, found := watched[filepath.Join(root, name)]; !found {
			t.Errorf("%s is not watched, so a dependency that moved rebuilds nothing", name)
		}
	}
}

// TestATreeIsTheSameUntilSomethingActuallyChanges keeps the loop from
// rebuilding on every tick.
//
// A comparison that answered "different" every time would close and reopen the
// window twice a second, which is not a development loop, it is a strobe.
func TestATreeIsTheSameUntilSomethingActuallyChanges(t *testing.T) {
	when := time.Now()
	before := map[string]time.Time{"a.go": when, "b.go": when}

	if !sameTree(before, map[string]time.Time{"a.go": when, "b.go": when}) {
		t.Error("an unchanged tree was reported as changed, and the window would reopen on every tick")
	}

	for name, after := range map[string]map[string]time.Time{
		"a file was edited":  {"a.go": when.Add(time.Second), "b.go": when},
		"a file was added":   {"a.go": when, "b.go": when, "c.go": when},
		"a file was removed": {"a.go": when},
	} {
		if sameTree(before, after) {
			t.Errorf("%s and the loop saw no change", name)
		}
	}
}

// TestAWindowThatWasNeverOpenedIsNotClosed keeps the failure path quiet.
//
// The first build of a session can fail -- a screen somebody is halfway
// through writing -- and the loop then has no window. Closing one anyway is a
// panic on a nil process, in a command whose whole job is to survive the edit
// that did not compile.
func TestAWindowThatWasNeverOpenedIsNotClosed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("closing a window that was never opened panicked: %v", r)
		}
	}()

	closeWindow(nil)
}

// TestTheBuiltTargetIsNotWrittenIntoTheSource keeps the loop's own output out
// of the tree it watches.
//
// A binary written beside the screens is a file the watcher sees, which
// rebuilds, which writes the binary again.
func TestTheBuiltTargetIsNotWrittenIntoTheSource(t *testing.T) {
	root := nativeProject(t, true)

	binary := filepath.Join(root, "bin", "native-dev")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, binary, "not really a binary")

	if _, found := nativeSnapshot(root)[binary]; found {
		t.Error("the built target is watched, and each build would trigger the next")
	}
}
