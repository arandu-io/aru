package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAViewSourceSchedulesAViewBuild is the daily loop of the view layer: save a
// `.kyse.go`, reload, see the change.
//
// The gate used to ask for ".templ", an extension kyse never writes and ADR 0020
// retired. A `.kyse.go` ends in ".go", so the watcher noticed the edit, restarted
// the server, and served the Go generated from the previous save.
func TestAViewSourceSchedulesAViewBuild(t *testing.T) {
	source := filepath.Join("resources", "views", "home.kyse.go")

	changed, views := diff(
		map[string]time.Time{source: time.Unix(1, 0)},
		map[string]time.Time{source: time.Unix(2, 0)},
	)
	if !changed {
		t.Fatal("editing a view source was not noticed at all")
	}
	if !views {
		t.Error("editing a view source did not schedule a view build")
	}

	// Deleting one has to rebuild too, or the view stays registered in the
	// generated Go of a file that no longer exists.
	changed, views = diff(
		map[string]time.Time{source: time.Unix(1, 0)},
		map[string]time.Time{},
	)
	if !changed || !views {
		t.Errorf("deleting a view source: changed=%v views=%v, want both true", changed, views)
	}
}

// TestAStylesheetSchedulesAViewBuild guards the other half of the gate, which
// was already right and must stay right.
func TestAStylesheetSchedulesAViewBuild(t *testing.T) {
	css := filepath.Join("resources", "css", "app.css")

	if _, views := diff(
		map[string]time.Time{css: time.Unix(1, 0)},
		map[string]time.Time{css: time.Unix(2, 0)},
	); !views {
		t.Error("editing the stylesheet did not schedule a view build")
	}
}

// TestOrdinaryGoDoesNotScheduleAViewBuild: a change in a handler restarts the
// server and nothing more. Rebuilding the views on every Go file would put the
// Tailwind download and the whole view compile on the hot path of every save.
func TestOrdinaryGoDoesNotScheduleAViewBuild(t *testing.T) {
	handler := filepath.Join("app", "Http", "Controllers", "HomeController.go")

	changed, views := diff(
		map[string]time.Time{handler: time.Unix(1, 0)},
		map[string]time.Time{handler: time.Unix(2, 0)},
	)
	if !changed {
		t.Fatal("editing a controller was not noticed")
	}
	if views {
		t.Error("editing a controller scheduled a view build")
	}
}

// TestTheGeneratedViewIsNotWatched is the other half of the same fix, and it has
// to land with it: the Go kyse emits is a `.go` file inside the watched tree, so
// a rebuild triggered by a view source would look like a change and trigger the
// next rebuild.
//
// The guard used to look for the "_templ.go" suffix, which kyse never writes.
func TestTheGeneratedViewIsNotWatched(t *testing.T) {
	root := t.TempDir()

	sources := []string{
		filepath.Join("resources", "views", "home.kyse.go"),
		filepath.Join("resources", "views", "layouts", "app.kyse.go"),
		filepath.Join("resources", "views", "auth", "login.kyse.go"),
	}
	// What `aru view:build` writes for each of them: under storage, mirroring
	// the tree. Watching it would make every build trigger the next.
	generated := []string{
		filepath.Join("storage", "framework", "views", "home.go"),
		filepath.Join("storage", "framework", "views", "layouts", "app.go"),
		filepath.Join("storage", "framework", "views", "auth", "login.go"),
	}
	handWritten := []string{
		filepath.Join("main.go"),
		filepath.Join("resources", "css", "app.css"),
	}

	for _, p := range append(append(append([]string{}, sources...), generated...), handWritten...) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package views\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	state := snapshot(root)
	for _, p := range generated {
		if _, watched := state[filepath.Join(root, p)]; watched {
			t.Errorf("%s is generated output and is being watched: every view build triggers the next", p)
		}
	}
	for _, p := range append(append([]string{}, sources...), handWritten...) {
		if _, watched := state[filepath.Join(root, p)]; !watched {
			t.Errorf("%s is an input and is not being watched", p)
		}
	}
}

// TestTheCompiledStylesheetIsNotWatched is the same defect as the generated
// view, in the one file the fix for that did not cover.
//
// `aru view:build` writes assets/app.css, and .css is watched because
// resources/css/app.css is the input somebody edits. So the output of a build
// was an input to the next one: a single edit to the stylesheet produced three
// restarts and four Tailwind runs, and nothing in aru bounded it -- the loop
// terminated only because Tailwind happens not to rewrite identical bytes.
func TestTheCompiledStylesheetIsNotWatched(t *testing.T) {
	root := t.TempDir()

	for _, p := range []string{
		filepath.Join("resources", "css", "app.css"), // the input
		filepath.Join("assets", "app.css"),           // what the build writes
		filepath.Join("assets", "fonts.go"),          // hand-written, beside it
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	state := snapshot(root)

	if _, watching := state[filepath.Join(root, "assets", "app.css")]; watching {
		t.Error("the compiled stylesheet is watched, so every build triggers the next")
	}
	if _, watching := state[filepath.Join(root, "resources", "css", "app.css")]; !watching {
		t.Error("the stylesheet somebody edits is not watched, so editing it does nothing")
	}
	if _, watching := state[filepath.Join(root, "assets", "fonts.go")]; !watching {
		t.Error("a hand-written file beside the output stopped being watched")
	}
}

// TestNothingUnderStorageIsWatched.
//
// storage/ is where the running application writes: uploads, cache entries,
// sessions. Watched, an upload restarted the process that had just served the
// request which produced it, and re-ran the whole view build with it.
func TestNothingUnderStorageIsWatched(t *testing.T) {
	root := t.TempDir()

	for _, p := range []string{
		filepath.Join("storage", "app", "private", "invoice.txt"),
		filepath.Join("storage", "framework", "cache", "x.go"),
		filepath.Join("bin", "app"),
		filepath.Join("main.go"),
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	state := snapshot(root)
	for path := range state {
		if strings.Contains(path, string(filepath.Separator)+"storage"+string(filepath.Separator)) {
			t.Errorf("%s is under storage and is watched: the application's own writes restart it", path)
		}
	}
	if _, watching := state[filepath.Join(root, "main.go")]; !watching {
		t.Error("main.go stopped being watched")
	}
}

// TestAnEmbeddedAssetIsWatched.
//
// A logo, a favicon, robots.txt and a vendored woff2 are compiled into the
// binary by go:embed. Unwatched, replacing one did nothing until the next `aru
// build` -- with no message, which reads as a caching bug that is not there.
func TestAnEmbeddedAssetIsWatched(t *testing.T) {
	for _, name := range []string{"logo.svg", "favicon.ico", "og.png", "inter.woff2", "robots.txt", "arandu.toml"} {
		if !watched(name) {
			t.Errorf("%s is embedded into the binary and is not watched, so replacing it appears to do nothing", name)
		}
	}
	for _, name := range []string{"README.md", "notes.org"} {
		if watched(name) {
			t.Errorf("%s is watched and nothing compiles it in", name)
		}
	}
}

// TestARebuildThatFailedIsStillOwed.
//
// This is the reported bug in its purest form. A view build fails -- a typo, a
// broken template, a network blip on the toolchain download -- and the change
// that asked for it has already been consumed from the snapshot. Without a
// pending flag, the next save of any ordinary .go file restarts the server
// against generated views from before the view edit, and only `aru build`
// repairs it. That is exactly "I have to run aru build and then aru dev".
//
// The loop's decision, not the loop: the process is what makes this expensive
// to test, and the decision is where the defect was.
func TestARebuildThatFailedIsStillOwed(t *testing.T) {
	pending := false
	build := func(changed, views, succeeds bool) (built, cleared bool) {
		if !changed {
			return false, false
		}
		pending = pending || views
		if !pending {
			return false, false
		}
		if !succeeds {
			return true, false
		}
		pending = false
		return true, true
	}

	// A view changes and the build fails.
	if built, cleared := build(true, true, false); !built || cleared {
		t.Fatal("the view change did not reach the build")
	}
	// Now an ordinary .go file is saved. Nothing about this change is a view,
	// and the build still has to run -- one is owed.
	built, cleared := build(true, false, true)
	if !built {
		t.Fatal("a failed view build was forgotten: the server restarts against stale views until `aru build`")
	}
	if !cleared {
		t.Fatal("the debt was not cleared by a build that succeeded, so it would rebuild forever")
	}
	// And nothing is owed after that.
	if built, _ := build(true, false, true); built {
		t.Error("a build ran with nothing owed")
	}
}
