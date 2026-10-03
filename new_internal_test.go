package main

import (
	"go/format"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const gitTraceEnv = "ARU_TEST_GIT_TRACE"

func TestMain(m *testing.M) {
	if trace := os.Getenv(gitTraceEnv); trace != "" && strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "git" {
		recordGitInvocation(trace, os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNewClonesThePublishedSkeletonRelease(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := "git"
	if runtime.GOOS == "windows" {
		git += ".exe"
	}
	copyExecutable(t, filepath.Join(bin, git))
	trace := filepath.Join(root, "git.trace")
	t.Setenv(gitTraceEnv, trace)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := newProject([]string{"my-app"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("aru new: %v", err)
	}

	body, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("read git invocation: %v", err)
	}
	got := strings.Split(string(body), "\x00")
	want := []string{
		"clone",
		"--branch", "v0.28.0",
		"--single-branch",
		"--depth", "1",
		"--quiet",
		"https://github.com/arandu-io/arandu.git",
		"my-app",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git arguments:\n  got  %q\n  want %q", got, want)
	}
}

// TestNewSaysWhatPostgresTakes pins the three lines the closing message gives
// for moving a project to Postgres.
//
// It promised one line in .env and nothing else, and that stops being true the
// moment the skeleton links only SQLite: DATABASE_URL alone then stops the boot
// with an error naming the two lines the message left out. Each line is matched
// whole, because a module path off by one element is a `go get` that fails and
// an import that does not resolve.
func TestNewSaysWhatPostgresTakes(t *testing.T) {
	got := createdMessage("my-app", "example.test/my-app")

	for _, line := range []string{
		"\n    go get github.com/arandu-io/hesape/database/connectors/pgx\n",
		"\n    _ \"github.com/arandu-io/hesape/database/connectors/pgx\"\n",
		"bootstrap/app.go",
		"\n    DATABASE_URL=postgres://user:password@127.0.0.1:5432/dbname\n",
		"\n    cd my-app\n",
		"module example.test/my-app.",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("the closing message does not say %q:\n%s", strings.TrimSpace(line), got)
		}
	}
	if strings.Contains(got, "nothing else") {
		t.Errorf("the closing message still promises Postgres takes one line:\n%s", got)
	}
}

func recordGitInvocation(trace string, args []string) {
	if err := os.WriteFile(trace, []byte(strings.Join(args, "\x00")), 0o600); err != nil {
		os.Exit(2)
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	destination := args[len(args)-1]
	if err := os.MkdirAll(destination, 0o755); err != nil {
		os.Exit(2)
	}
	env := "APP_KEY=\n"
	if err := os.WriteFile(filepath.Join(destination, ".env.example"), []byte(env), 0o600); err != nil {
		os.Exit(2)
	}
}

func copyExecutable(t *testing.T, target string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDropRetractionsLeavesTheProjectOnlyItsOwnDirectives pins that a new
// project does not inherit what the skeleton says about its own releases, in
// either the one-line or the block form, and that nothing else in go.mod moves.
func TestDropRetractionsLeavesTheProjectOnlyItsOwnDirectives(t *testing.T) {
	dir := t.TempDir()
	skeleton := "module example.test/app\n\ngo 1.26.0\n\nretract v0.10.0 // Requires retracted Kyse v0.15.1.\n\nretract (\n\tv0.3.0\n\t[v0.4.0, v0.4.2]\n)\n\nrequire github.com/arandu-io/framework v0.50.2\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(skeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetractions(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	want := "module example.test/app\n\ngo 1.26.0\n\nrequire github.com/arandu-io/framework v0.50.2\n"
	if string(got) != want {
		t.Fatalf("go.mod after dropRetractions:\n%s\nwant:\n%s", got, want)
	}
}

// TestRewriteModulePathLeavesTheProjectGofmtClean pins that renaming the module
// does not hand somebody a project their own gofmt gate refuses.
//
// The skeleton sorts its imports with github.com/arandu-io/arandu among the
// other github.com paths. A project called kuaa.io/app sorts after all of them,
// so a textual rename leaves the group out of order and gofmt -l lists the
// file -- in a project nobody has edited yet.
//
// A view source and a fixture under testdata are renamed and left as they were
// otherwise: the first is not Go until it is compiled, and the second may be
// wrong on purpose.
func TestRewriteModulePathLeavesTheProjectGofmtClean(t *testing.T) {
	dir := t.TempDir()
	controller := "package controllers\n\nimport (\n\t\"github.com/arandu-io/arandu/app/Services\"\n\t\"github.com/arandu-io/framework/security\"\n\t\"github.com/arandu-io/hesape/http\"\n)\n\nvar _ = services.New\nvar _ security.Grant\nvar _ http.Request\n"
	view := "//go:build kyse\n\npackage views\n\nimport (\n\t\"github.com/arandu-io/arandu/app/Models\"\n\t\"github.com/arandu-io/framework/view\"\n)\n\n@extends('layouts.app')\n"
	fixture := "package fixture\n\nimport (\n\t\"github.com/arandu-io/arandu/app/Models\"\n\t\"github.com/arandu-io/framework/data\"\n)\n"
	files := map[string]string{
		"go.mod": "module github.com/arandu-io/arandu\n\ngo 1.26.0\n",
		filepath.Join("app", "Http", "Controllers", "HomeController.go"): controller,
		filepath.Join("resources", "views", "home.kyse.go"):              view,
		filepath.Join("tests", "testdata", "fixture.go"):                 fixture,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := rewriteModulePath(dir, "kuaa.io/app"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "app", "Http", "Controllers", "HomeController.go"))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(got)
	if err != nil {
		t.Fatalf("the renamed controller does not parse: %v\n%s", err, got)
	}
	if string(formatted) != string(got) {
		t.Errorf("the renamed controller is not gofmt-clean:\n%s\nwant:\n%s", got, formatted)
	}
	if !strings.Contains(string(got), `"kuaa.io/app/app/Services"`) {
		t.Errorf("the import was not renamed:\n%s", got)
	}

	for name, before := range map[string]string{
		filepath.Join("resources", "views", "home.kyse.go"): view,
		filepath.Join("tests", "testdata", "fixture.go"):    fixture,
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(before, "github.com/arandu-io/arandu", "kuaa.io/app")
		if string(got) != want {
			t.Errorf("%s was changed beyond the rename:\n%s\nwant:\n%s", name, got, want)
		}
	}

	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "module kuaa.io/app\n\ngo 1.26.0\n"; string(mod) != want {
		t.Errorf("go.mod after the rename:\n%s\nwant:\n%s", mod, want)
	}
}
