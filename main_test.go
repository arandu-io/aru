package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exercise(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoArgumentsPrintsUsageAndFails(t *testing.T) {
	code, stdout, _ := exercise(t)

	if code == 0 {
		t.Error("running with no command must exit non-zero, so scripts notice")
	}
	for _, want := range []string{"usage: aru <command>", "key:generate", "serve", "migrate", "routes"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}

// TestUsageIsSober checks the tone rule from 04-marca.md at the only place users
// actually read it.
func TestUsageIsSober(t *testing.T) {
	_, stdout, _ := exercise(t, "help")

	for _, forbidden := range []string{"!", "🚀", "✨", "🎉"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("the usage text contains %q", forbidden)
		}
	}
}

func TestHelpAndVersionSucceed(t *testing.T) {
	if code, _, _ := exercise(t, "help"); code != 0 {
		t.Errorf("help exited %d, want 0", code)
	}
	code, stdout, _ := exercise(t, "version")
	if code != 0 {
		t.Errorf("version exited %d, want 0", code)
	}
	if !strings.HasPrefix(stdout, "aru ") {
		t.Errorf("version output = %q", stdout)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	code, _, stderr := exercise(t, "migreate")

	if code == 0 {
		t.Error("an unknown command exited 0")
	}
	if !strings.Contains(stderr, "unknown command: migreate") {
		t.Errorf("stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "migrate") {
		t.Error("the error must be followed by the command list, so the typo is visible")
	}
}

// TestKeyGenerateProducesAUsableKey pins the contract with config.parseAppKey: the
// prefix and the decoded length are what the framework validates at boot.
func TestKeyGenerateProducesAUsableKey(t *testing.T) {
	code, stdout, stderr := exercise(t, "key:generate")

	if code != 0 {
		t.Fatalf("exited %d: %s", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	encoded, ok := strings.CutPrefix(line, "APP_KEY=base64:")
	if !ok {
		t.Fatalf("output = %q, want an APP_KEY=base64: line", line)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the key is not valid base64: %v", err)
	}
	if len(key) != appKeyLen {
		t.Fatalf("key length = %d, want %d", len(key), appKeyLen)
	}
	// The warning goes to stderr so that `aru key:generate >> .env` stays clean.
	if !strings.Contains(stderr, "invalidates every session") {
		t.Error("the command must warn what rotating the key costs")
	}
	if strings.Contains(stdout, "invalidates") {
		t.Error("the warning must not go to stdout: it would end up inside .env")
	}
}

func TestKeyGenerateIsRandom(t *testing.T) {
	_, first, _ := exercise(t, "key:generate")
	_, second, _ := exercise(t, "key:generate")

	if first == second {
		t.Fatal("two generated keys are identical")
	}
}

func TestKeyGenerateRejectsArguments(t *testing.T) {
	if code, _, _ := exercise(t, "key:generate", "--write"); code == 0 {
		t.Error("an unsupported flag was silently ignored")
	}
}

// TestPhaseTwoCommandsSaySo: refusing with the phase number is more useful than
// a command that appears to work and changes nothing.
func TestPhaseTwoCommandsSaySo(t *testing.T) {
	for _, name := range []string{"new", "make:module", "make:policy", "doctor"} {
		code, _, stderr := exercise(t, name)
		if code == 0 {
			t.Errorf("%s exited 0 without doing anything", name)
		}
		if !strings.Contains(stderr, "phase 2") {
			t.Errorf("%s does not state its phase: %q", name, stderr)
		}
	}
}

// TestDelegationRequiresAProject covers the most common mistake with these three
// commands: running them from the wrong directory.
func TestDelegationRequiresAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, name := range []string{"serve", "migrate", "routes", "seed:admin"} {
		code, _, stderr := exercise(t, name)
		if code == 0 {
			t.Errorf("%s exited 0 outside a project", name)
		}
		if !strings.Contains(stderr, "cmd/app") {
			t.Errorf("%s does not explain what is missing: %q", name, stderr)
		}
	}
}

// TestProjectRootIsFoundFromASubdirectory: the commands have to work from
// wherever the developer happens to be inside the project.
func TestProjectRootIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "modules", "billing")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)

	got, err := projectRoot()
	if err != nil {
		t.Fatalf("projectRoot: %v", err)
	}

	// macOS reports the temporary directory through a symlink, so compare the
	// resolved paths.
	wantResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("projectRoot = %q, want %q", gotResolved, wantResolved)
	}
}

// TestCommandNamesAreUnique guards the dispatch table itself.
func TestCommandNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		if seen[c.name] {
			t.Errorf("duplicate command: %s", c.name)
		}
		seen[c.name] = true
		if c.desc == "" || c.usage == "" || c.run == nil {
			t.Errorf("command %s is incomplete", c.name)
		}
	}
}
