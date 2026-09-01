package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI renders what the project binary answers, so most of these drive the
// renderer with a payload. The last one runs a real project, because the
// handover is the half a payload cannot prove.

// sampleKey is a value with the shape of a real application key: the prefix and
// thirty-two bytes once decoded. Built rather than pasted, so nothing that looks
// like a credential is committed.
func sampleKey() string {
	return "base64:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

// aboutPayload is what a wired application reports.
func aboutPayload(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{
  "Sections": [
    {"Name": "Environment", "Entries": [
      {"Name": "Application Name", "Value": "Acme"},
      {"Name": "Environment", "Value": "local"},
      {"Name": "URL", "Value": "http://127.0.0.1:8080"},
      {"Name": "Version", "Value": "v0.4.1 (9f2c1ab)"},
      {"Name": "Application Key", "Value": %q, "Secret": true}
    ]},
    {"Name": "Drivers", "Entries": [
      {"Name": "Cache", "Value": "redis"},
      {"Name": "Database", "Value": "postgres"},
      {"Name": "Mail", "Value": "smtp"},
      {"Name": "Queue", "Value": "database"},
      {"Name": "Session", "Value": "kv"}
    ]},
    {"Name": "Modules", "Entries": [
      {"Name": "auth", "Value": "9 routes"},
      {"Name": "invoice", "Value": "6 routes"}
    ]}
  ]
}`, sampleKey())
}

func renderAbout(t *testing.T, payload, only string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := printAbout(&out, []byte(payload), only)
	return out.String(), err
}

// TestTheReportShowsWhatIsWired is the reason the command exists: one screen
// that answers "what is this application running on".
func TestTheReportShowsWhatIsWired(t *testing.T) {
	out, err := renderAbout(t, aboutPayload(t), "")
	if err != nil {
		t.Fatalf("about: %v", err)
	}

	for _, want := range []string{
		"Environment", "Acme", "local", "http://127.0.0.1:8080", "v0.4.1 (9f2c1ab)",
		"Drivers", "Cache", "redis", "Database", "postgres", "Queue", "database", "Session", "kv",
		"Modules", "auth", "invoice",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not show %q:\n%s", want, out)
		}
	}

	// The order the application chose is the order printed: a report whose
	// sections shuffle between runs cannot be diffed against an earlier one.
	if strings.Index(out, "Environment") > strings.Index(out, "Drivers") {
		t.Errorf("the sections were reordered:\n%s", out)
	}
}

// TestTheApplicationKeyIsRedacted is the whole reason this command needed a
// rule of its own. What it prints is what somebody pastes into a bug report,
// and a key that reaches one has to be rotated -- which invalidates every
// session and everything encrypted with it.
func TestTheApplicationKeyIsRedacted(t *testing.T) {
	key := sampleKey()

	out, err := renderAbout(t, aboutPayload(t), "")
	if err != nil {
		t.Fatalf("about: %v", err)
	}

	if strings.Contains(out, key) {
		t.Fatalf("the application key was printed:\n%s", out)
	}
	// The encoded half alone, in case a renderer ever prints the value without
	// its prefix.
	if encoded, _ := strings.CutPrefix(key, "base64:"); strings.Contains(out, encoded) {
		t.Fatalf("the encoded application key was printed:\n%s", out)
	}
	if !strings.Contains(out, redacted) {
		t.Errorf("nothing says the key was withheld:\n%s", out)
	}
	// Withheld, not omitted: the line has to be there, or the report reads like
	// an application with no key configured.
	if !strings.Contains(out, "Application Key") {
		t.Errorf("the key is not mentioned at all:\n%s", out)
	}
}

// TestAKeyTheApplicationDidNotMarkIsStillRedacted: the payload declares what is
// secret, and this is the floor under that declaration. An application that
// ships the one credential every project has without the flag must not be able
// to print it here.
func TestAKeyTheApplicationDidNotMarkIsStillRedacted(t *testing.T) {
	key := sampleKey()
	payload := fmt.Sprintf(`{"Sections": [
	  {"Name": "Environment", "Entries": [{"Name": "Application Key", "Value": %q}]}
	]}`, key)

	out, err := renderAbout(t, payload, "")
	if err != nil {
		t.Fatalf("about: %v", err)
	}
	if strings.Contains(out, key) {
		t.Fatalf("an unmarked application key was printed:\n%s", out)
	}
	if !strings.Contains(out, redacted) {
		t.Errorf("the value was neither printed nor withheld:\n%s", out)
	}
}

// TestOrdinaryValuesAreNotRedacted guards the other side of the shape check: a
// report where everything is withheld reports nothing.
func TestOrdinaryValuesAreNotRedacted(t *testing.T) {
	payload := `{"Sections": [
	  {"Name": "Drivers", "Entries": [
	    {"Name": "Cache", "Value": "redis"},
	    {"Name": "Mail", "Value": "base64:not-a-key"}
	  ]}
	]}`

	out, err := renderAbout(t, payload, "")
	if err != nil {
		t.Fatalf("about: %v", err)
	}
	for _, want := range []string{"redis", "base64:not-a-key"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q was withheld, and it is not a secret:\n%s", want, out)
		}
	}
}

// TestAKeyThatIsNotSetSaysSo: a missing key and a present one fail in
// completely different ways, and saying which leaks nothing.
func TestAKeyThatIsNotSetSaysSo(t *testing.T) {
	payload := `{"Sections": [
	  {"Name": "Environment", "Entries": [{"Name": "Application Key", "Value": "", "Secret": true}]}
	]}`

	out, err := renderAbout(t, payload, "")
	if err != nil {
		t.Fatalf("about: %v", err)
	}
	if !strings.Contains(out, "not set") {
		t.Errorf("a missing key is not reported as missing:\n%s", out)
	}
	if strings.Contains(out, redacted) {
		t.Errorf("an empty secret was reported as if something was withheld:\n%s", out)
	}
}

func TestOnlyRestrictsTheReportToOneSection(t *testing.T) {
	payload := aboutPayload(t)

	// Lower case, because that is how a section is typed at a shell.
	out, err := renderAbout(t, payload, "drivers")
	if err != nil {
		t.Fatalf("about --only=drivers: %v", err)
	}
	if !strings.Contains(out, "Drivers") || !strings.Contains(out, "postgres") {
		t.Errorf("--only=drivers did not print the section:\n%s", out)
	}
	for _, absent := range []string{"Application Name", "Modules", "invoice"} {
		if strings.Contains(out, absent) {
			t.Errorf("--only=drivers still printed %q:\n%s", absent, out)
		}
	}

	// The name as the report spells it works too.
	if out, err := renderAbout(t, payload, "Modules"); err != nil {
		t.Fatalf("about --only=Modules: %v", err)
	} else if !strings.Contains(out, "invoice") || strings.Contains(out, "postgres") {
		t.Errorf("--only=Modules selected the wrong section:\n%s", out)
	}
}

// TestAnUnknownSectionNamesTheOnesThatExist: the typo is the common case, and
// the answer that ends it is the list.
func TestAnUnknownSectionNamesTheOnesThatExist(t *testing.T) {
	_, err := renderAbout(t, aboutPayload(t), "driver")
	if err == nil {
		t.Fatal("an unknown section was accepted, so the report printed everything")
	}
	for _, want := range []string{"Environment", "Drivers", "Modules"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %q: %v", want, err)
		}
	}
}

// TestGarbageFromTheProjectIsNotAPanic: an older project answers this
// subcommand with something else entirely, and a stack trace would be a worse
// answer than a sentence.
func TestGarbageFromTheProjectIsNotAPanic(t *testing.T) {
	if _, err := renderAbout(t, "unknown command: about\n", ""); err == nil {
		t.Fatal("a line of prose was accepted as a report")
	}
	if _, err := renderAbout(t, `{"Sections": []}`, ""); err == nil {
		t.Fatal("an empty report was printed as if it said something")
	}
}

// TestAboutRefusesArgumentsItDoesNotUnderstand is the measured trap: the
// project's own console reads the arguments of a listing command and ignores
// them, and a report narrowed by nothing looks exactly like a report narrowed
// by what was typed.
//
// Every refusal here happens before the project is compiled, which is why they
// can be asserted from an empty directory.
func TestAboutRefusesArgumentsItDoesNotUnderstand(t *testing.T) {
	t.Chdir(t.TempDir())

	// A section named without the flag. The suggestion is the whole value of
	// the message: this is the way everybody types it the first time.
	code, _, stderr := exercise(t, "about", "drivers")
	if code == 0 {
		t.Error("a bare section name was swallowed")
	}
	if !strings.Contains(stderr, "--only=drivers") {
		t.Errorf("the error does not say how to write it: %q", stderr)
	}

	// A flag this command does not have.
	if code, _, _ := exercise(t, "about", "--sections=all"); code == 0 {
		t.Error("an unknown flag was swallowed")
	}

	// The flag with no value: it asked for a section and named none.
	code, _, stderr = exercise(t, "about", "--only=")
	if code == 0 {
		t.Error("--only with no section printed the whole report")
	}
	if !strings.Contains(stderr, "section") {
		t.Errorf("the error does not say what is missing: %q", stderr)
	}

	// And the flag itself is accepted: the failure below is the missing
	// project, which is what proves the parsing got out of the way.
	code, _, stderr = exercise(t, "about", "--only=drivers")
	if code == 0 {
		t.Error("about ran outside a project")
	}
	if !strings.Contains(stderr, "arandu.toml") {
		t.Errorf("--only=drivers was rejected as a bad argument: %q", stderr)
	}
}

// TestTheReportComesFromTheProjectBinary runs a project that answers this
// subcommand, and reads what reaches the terminal.
//
// It is the half no payload can prove: that the subcommand handed over is
// "about", that the project is handed no arguments of its own, and that the
// report is read off standard output rather than mixed with whatever the build
// wrote to standard error.
//
// Three files, because projectRoot requires all three together, and no go
// directive: a version above the local toolchain would send `go run` looking
// for another one before it reached this.
func TestTheReportComesFromTheProjectBinary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so nothing could be delegated")
	}

	root := t.TempDir()
	main := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\n" +
		"const payload = `" + aboutPayload(t) + "`\n\n" +
		"func main() {\n" +
		"\tif got := strings.Join(os.Args[1:], \" \"); got != \"about\" {\n" +
		"\t\tfmt.Fprintf(os.Stderr, \"the project binary was called with %q\\n\", got)\n" +
		"\t\tos.Exit(1)\n" +
		"\t}\n" +
		"\tfmt.Fprintln(os.Stderr, \"a line the build wrote\")\n" +
		"\tfmt.Println(payload)\n" +
		"}\n"

	for name, body := range map[string]string{
		"go.mod":      "module example.test/about\n",
		"arandu.toml": "name = \"about\"\n",
		"main.go":     main,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The workspace is switched off for the child. This repository sits inside
	// one, and a temporary directory is not a module of it, so `go run` would
	// refuse before it ever compiled anything.
	t.Setenv("GOWORK", "off")
	t.Chdir(root)

	code, stdout, stderr := exercise(t, "about")
	if code != 0 {
		t.Fatalf("about exited %d inside a project: %s", code, stderr)
	}
	for _, want := range []string{"Environment", "Drivers", "postgres", "Modules", "invoice", redacted} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not show %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, sampleKey()) {
		t.Errorf("the application key reached the terminal:\n%s", stdout)
	}
	// What the project wrote to standard error stays out of the report, so
	// `aru about > report.txt` collects the inventory and nothing else.
	if strings.Contains(stdout, "a line the build wrote") {
		t.Errorf("standard error was folded into the report:\n%s", stdout)
	}

	// The section filter over the real handover, so the flag is proved on the
	// path people use rather than only against a payload.
	code, stdout, stderr = exercise(t, "about", "--only=drivers")
	if code != 0 {
		t.Fatalf("about --only=drivers exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "postgres") || strings.Contains(stdout, "invoice") {
		t.Errorf("--only=drivers did not restrict the report:\n%s", stdout)
	}
}
