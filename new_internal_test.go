package main

import (
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
		"--branch", "v0.27.1",
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
