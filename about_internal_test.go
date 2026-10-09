package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
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
      {"Name": "Session", "Value": "redis"}
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
	var report aboutReport
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		t.Fatalf("the sample report does not decode: %v", err)
	}
	var out strings.Builder
	err := printAbout(&out, report, only)
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
		"Drivers", "Cache", "redis", "Database", "postgres", "Queue", "database", "Session", "redis",
		"Modules", "auth", "invoice",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not show %q:\n%s", want, out)
		}
	}
	var sessionRow string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "Session" {
			sessionRow = strings.Join(fields, " ")
		}
	}
	if sessionRow != "Session redis" {
		t.Errorf("session driver row = %q, want Session redis", sessionRow)
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

// TestAnEmptyReportIsRefused: a report with nothing in it printed as if it
// said something is the one output that cannot be told apart from a project
// with nothing wired.
func TestAnEmptyReportIsRefused(t *testing.T) {
	if _, err := renderAbout(t, `{"Sections": []}`, ""); err == nil {
		t.Fatal("an empty report was printed as if it said something")
	}
}

// TestAboutRefusesArgumentsItDoesNotUnderstand is the measured trap: the
// project's own console reads the arguments of a listing command and ignores
// them, and a report narrowed by nothing looks exactly like a report narrowed
// by what was typed.
//
// Every refusal here happens before the project is read, which is why they can
// be asserted from an empty directory.
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

// TestTheReportIsReadFromTheProject builds the report from a project on disk,
// the way every existing project is read: nothing in it answers a subcommand,
// and the inventory still comes back.
//
// The environment is pinned for each setting the report reads, so a variable
// the machine running the test exports cannot change the answer -- and one of
// them is set on purpose, because the environment winning over .env is the
// precedence the boot applies and the report has to agree with it.
func TestTheReportIsReadFromTheProject(t *testing.T) {
	for _, s := range append(append([]aboutSetting{}, aboutEnvironment...), aboutDrivers...) {
		t.Setenv(s.name, "")
		if err := os.Unsetenv(s.name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CACHE_STORE", "memory")

	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module example.test/about\n\ngo 1.26.0\n\nrequire (\n" +
			"\tgithub.com/arandu-io/framework v0.50.2\n\tgithub.com/arandu-io/hesape v0.48.0\n\tgithub.com/arandu-io/kyse v0.30.0\n)\n",
		"arandu.toml": "name = \"about\"\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		".env": "APP_NAME=Acme\nAPP_ENV=local\nAPP_URL=http://127.0.0.1:8080\nAPP_KEY=" + sampleKey() + "\n" +
			"DATABASE_URL=postgres://user:hunter2@127.0.0.1:5432/acme\nCACHE_STORE=redis\nQUEUE_CONNECTION=database\n",
		"bootstrap/app.go": `package bootstrap

import (
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/cache"

	audit "example.org/community/audit"
)

var _ cache.Store

func Boot(app *foundation.Application) {
	app.Register(
		audit.NewModule(),
	)
}
`,
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	code, stdout, stderr := exercise(t, "about")
	if code != 0 {
		t.Fatalf("about exited %d inside a project: %s", code, stderr)
	}
	for _, want := range []string{
		"Environment", "Acme", "local", "http://127.0.0.1:8080", redacted,
		"Versions", "example.test/about", "1.26.0", "v0.50.2", "v0.48.0", "v0.30.0",
		"Drivers", "pgsql", "database", notSet,
		"Modules", "example.org/community/audit", "registered in bootstrap, bootstrap/app.go:14",
		"Components", "cache",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not show %q:\n%s", want, stdout)
		}
	}
	for _, leak := range []string{sampleKey(), "hunter2", "postgres://"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("the report printed %q, which is a credential or carries one:\n%s", leak, stdout)
		}
	}
	if cache := rowOf(stdout, "Cache"); !strings.Contains(cache, "memory") {
		t.Errorf("CACHE_STORE is exported as memory and .env says redis, and the report shows %q: "+
			"the environment wins at boot", cache)
	}

	code, stdout, stderr = exercise(t, "about", "--only=drivers")
	if code != 0 {
		t.Fatalf("about --only=drivers exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "pgsql") || strings.Contains(stdout, "Acme") || strings.Contains(stdout, "audit") {
		t.Errorf("--only=drivers did not restrict the report:\n%s", stdout)
	}
}

// rowOf is the line of a report whose first field is label.
func rowOf(report, label string) string {
	for _, line := range strings.Split(report, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == label {
			return line
		}
	}
	return ""
}
