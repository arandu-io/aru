package main

import (
	"os"
	"path/filepath"
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
