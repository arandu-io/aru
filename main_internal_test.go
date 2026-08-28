package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
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
	for _, want := range []string{"usage: aru <command>", "key:generate", "serve", "migrate", "route:list"} {
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
	// Thirty-two written out, not appKeyLen. A test that asserts a length
	// against the constant the code generates with asserts nothing: change the
	// constant and the assertion moves with it. This is the number the framework
	// requires, so changing it has to be done twice, on purpose.
	//
	// It is the only guard there is. Nothing here can reach the framework's
	// constant -- it is unexported, and this is a separate module -- so a change
	// on that side still passes.
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
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

// TestEveryCommandIsImplemented: every command in the list runs, and this test
// is what will notice if one of them ever regresses to a stub.
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

	for _, name := range []string{"serve", "migrate", "migrate:rollback", "migrate:status", "migrate:fresh", "route:list", "db:seed"} {
		code, _, stderr := exercise(t, name)
		if code == 0 {
			t.Errorf("%s exited 0 outside a project", name)
		}
		if !strings.Contains(stderr, "arandu.toml") {
			t.Errorf("%s does not explain what is missing: %q", name, stderr)
		}
	}
}

// probeProject writes a project whose binary reports the arguments it was
// handed, and moves into it.
//
// It is what makes a forwarded command testable at all. `aru queue:retry` does
// exactly one thing here -- hand an argv to the project binary -- and the only
// place that argv is readable is inside the binary that receives it. Asserting
// that the entry exists in the slice would pass on a command wired to the wrong
// subcommand, which is the mistake worth catching.
//
// Three files, because projectRoot requires all three together, and no go
// directive: a version above the local toolchain would send `go run` looking for
// another one before it reached the probe.
func probeProject(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example.test/probe\n",
		"arandu.toml": "name = \"probe\"\n",
		"main.go": `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println("argv:", strings.Join(os.Args[1:], " ")) }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The workspace is switched off for the child. This repository sits inside
	// one, and a temporary directory is not a module of it, so `go run` would
	// refuse before it ever compiled the probe.
	t.Setenv("GOWORK", "off")
	t.Chdir(root)
}

// TestTheQueueCommandsReachTheProject runs every queue command and reads what
// arrived at the other end.
//
// The failed job list, the batch list and the queue are the application's
// tables, so all fourteen of these forward and none of them can be answered
// here. What this repository is responsible for is the handover, and the way it
// breaks is silent: a command wired to a subcommand nobody dispatches still
// appears in `aru help`, still exits, and still says nothing about the job that
// died.
//
// The expectations are derived from the slice rather than listed again, so a
// queue command added later is exercised by this test on the day it is added
// rather than on the day somebody remembers it.
func TestTheQueueCommandsReachTheProject(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so nothing could be delegated")
	}
	probeProject(t)

	// The one command whose forwarded name is not the name typed. The word the
	// project binary receives is an internal protocol that promises nothing, and
	// changing it would break every project generated before today.
	renamed := map[string]string{"queue:work": "work"}

	queued := 0
	for _, c := range commands {
		if !strings.HasPrefix(c.name, "queue:") {
			continue
		}
		queued++

		t.Run(c.name, func(t *testing.T) {
			want := c.name
			if other, ok := renamed[c.name]; ok {
				want = other
			}

			// A flag nobody here parses, to prove the arguments are passed
			// through rather than consumed: every one of these takes flags this
			// binary knows nothing about.
			code, stdout, stderr := exercise(t, c.name, "--tenant=t1")
			if code != 0 {
				t.Fatalf("%s exited %d inside a project: %s", c.name, code, stderr)
			}
			if got, want := stdout, "argv: "+want+" --tenant=t1\n"; got != want {
				t.Errorf("the project binary received %q, want %q", got, want)
			}
		})
	}

	// Every check above is true of no queue command at all, and a filter that
	// matched nothing would report a pass over an empty loop.
	if queued == 0 {
		t.Fatal("no command in the slice is a queue command, so nothing was forwarded")
	}
}

// TestTheDeadLetterCommandsAreReachable is the gap this family closes, named.
//
// A job that gave up is inspected with queue:failed and put back with
// queue:retry, and for as long as neither was in the slice the answer to a dead
// job was SQL by hand. The five are asserted by name because that is the claim:
// not that the queue family is large, but that these particular five can be
// typed.
func TestTheDeadLetterCommandsAreReachable(t *testing.T) {
	for _, name := range []string{"queue:failed", "queue:retry", "queue:forget", "queue:flush", "queue:prune-failed"} {
		c, found := lookup(name)
		if !found {
			t.Errorf("%s is not a command, so a failed job cannot be reached from the CLI", name)
			continue
		}
		// --tenant, because a failed job list is one customer's and the command
		// that answered without one would print whichever sorted first.
		if !strings.Contains(c.usage, "--tenant") {
			t.Errorf("%s does not show --tenant in its usage: %q", name, c.usage)
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

// TestDoctorTakesAProfile pins the flag at the level a pipeline uses it.
//
// A rule test proves the checks work; this proves they are reachable. A check
// nobody can ask for is a check nobody runs.
func TestDoctorTakesAProfile(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"arandu.toml": "name = \"test\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	code, stdout, stderr := exercise(t, "doctor", "--profile=performance")
	if code != 0 {
		t.Fatalf("doctor --profile=performance exited %d: %s", code, stderr)
	}
	// The profile is in the summary, so a clean report cannot be mistaken for the
	// other profile's clean report.
	if !strings.Contains(stdout, "performance") {
		t.Errorf("a clean run does not say which profile produced it: %q", stdout)
	}

	if code, _, _ := exercise(t, "doctor", "--profile=conventional"); code != 0 {
		t.Error("the default profile is not accepted by name, so a pipeline cannot say which one it meant")
	}

	code, _, stderr = exercise(t, "doctor", "--profile=fast")
	if code == 0 {
		t.Error("an unknown profile exited 0: a typo would check less than was asked for and say nothing")
	}
	if !strings.Contains(stderr, "performance") {
		t.Errorf("the error does not name the values that exist: %q", stderr)
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
