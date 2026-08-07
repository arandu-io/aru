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

// TestEveryCommandIsImplemented: the phase 2 commands are all in. What is left
// of the phase lives in other repositories -- the adapters -- and this test is
// what will notice if one of these ever regresses to a stub.
func TestEveryCommandIsImplemented(t *testing.T) {
	for _, c := range commands {
		out := &bytes.Buffer{}
		_ = run([]string{c.name}, out, out)
		if strings.Contains(out.String(), "not implemented") {
			t.Errorf("%s still refuses with a phase number", c.name)
		}
	}
}

// TestMakeModuleRequiresFieldsAndAProject covers the two ways to get the command
// wrong: running it outside a project, and forgetting what the module holds.
func TestMakeModuleRequiresFieldsAndAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	code, _, stderr := exercise(t, "make:module", "invoice")
	if code == 0 {
		t.Error("make:module ran outside a project")
	}
	if !strings.Contains(stderr, "arandu.toml") {
		t.Errorf("the error does not say what is missing: %q", stderr)
	}

	code, _, stderr = exercise(t, "make:module")
	if code == 0 {
		t.Error("make:module ran with no name")
	}
	if !strings.Contains(stderr, "usage:") && !strings.Contains(stderr, "arandu.toml") {
		t.Errorf("the error is not actionable: %q", stderr)
	}
}

// TestNewRefusesWhatItCannotDo covers the two failures that happen before any
// network call, which are the ones worth failing fast on.
func TestNewRefusesWhatItCannotDo(t *testing.T) {
	t.Chdir(t.TempDir())

	if code, _, stderr := exercise(t, "new"); code == 0 {
		t.Error("new ran with no name")
	} else if !strings.Contains(stderr, "usage: aru new") {
		t.Errorf("the error is not actionable: %q", stderr)
	}

	if code, _, stderr := exercise(t, "new", "some/path"); code == 0 {
		t.Error("new accepted a name with a path separator")
	} else if !strings.Contains(stderr, "path separator") {
		t.Errorf("the error does not say what is wrong: %q", stderr)
	}

	if err := os.Mkdir("taken", 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := exercise(t, "new", "taken"); code == 0 {
		t.Error("new overwrote an existing directory")
	} else if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestDelegationRequiresAProject covers the most common mistake with these three
// commands: running them from the wrong directory.
func TestDelegationRequiresAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, name := range []string{"serve", "migrate", "migrate:rollback", "migrate:status", "migrate:fresh", "routes", "db:seed"} {
		code, _, stderr := exercise(t, name)
		if code == 0 {
			t.Errorf("%s exited 0 outside a project", name)
		}
		if !strings.Contains(stderr, "arandu.toml") {
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
	// The three files that together say "this is an Arandu project": go.mod is
	// any Go module, main.go is any program, and arandu.toml is what tells it
	// apart -- without it, running `aru` inside an unrelated project would walk
	// up and act on it.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "arandu.toml"), []byte("name = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "app", "Http", "Controllers")
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

// TestTheGeneratedEnvLetsTheThreePrintedCommandsRun is the regression guard for
// an instruction that did not work.
//
// `aru new` prints three commands to run next -- migrate, db:seed, serve -- and
// db:seed refused with "set ARANDU_ADMIN_EMAIL and ARANDU_ADMIN_PASSWORD before
// seeding the administrator". The seeder was right: an administrator with a
// default password is an administrator everyone has. What was wrong is that
// nothing told the reader the variables existed, and nothing wrote them.
//
// The password is generated per project, like APP_KEY. A constant here would be
// the default password the refusal exists to prevent.
func TestTheGeneratedEnvLetsTheThreePrintedCommandsRun(t *testing.T) {
	generated := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".env.example"),
			[]byte("APP_KEY=\nARANDU_ADMIN_EMAIL=\nARANDU_ADMIN_PASSWORD=\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeEnv(dir); err != nil {
			t.Fatalf("writeEnv: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	env := generated(t)
	for _, key := range []string{"APP_KEY=base64:", "ARANDU_ADMIN_EMAIL=", "ARANDU_ADMIN_PASSWORD="} {
		i := strings.Index(env, key)
		if i < 0 {
			t.Errorf("%s is missing from the generated .env", key)
			continue
		}
		// The key has to carry a value. An empty one is exactly the state that
		// made db:seed refuse.
		if line, _, _ := strings.Cut(env[i+len(key):], "\n"); strings.TrimSpace(line) == "" {
			t.Errorf("%s was written with no value", key)
		}
	}

	// Two projects must not share an administrator password.
	if adminPassword(t, env) == adminPassword(t, generated(t)) {
		t.Error("two projects got the same administrator password")
	}
}

func adminPassword(t *testing.T, env string) string {
	t.Helper()
	_, rest, ok := strings.Cut(env, "ARANDU_ADMIN_PASSWORD=")
	if !ok {
		t.Fatal("no ARANDU_ADMIN_PASSWORD in the generated .env")
	}
	line, _, _ := strings.Cut(rest, "\n")
	return line
}
