package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/framework/test/browser/cdp"
)

// TestInstallBrowsersNamesTheNativeDriversBrowser runs the command in a
// project that does not require the Playwright driver: nothing is installed,
// and the answer is the browser the native driver found.
func TestInstallBrowsersNamesTheNativeDriversBrowser(t *testing.T) {
	probeProjectRunning(t, "package main\n\nfunc main() {}\n")
	code, stdout, stderr := exercise(t, "e2e:install-browsers")
	if path, err := cdp.Find(); err == nil {
		if code != 0 || !strings.Contains(stdout, "uses the installed browser at "+path) {
			t.Errorf("e2e:install-browsers did not name %s: %s %s", path, stdout, stderr)
		}
	} else if code == 0 || !strings.Contains(stderr, "CHROME_PATH") {
		t.Errorf("with no browser, e2e:install-browsers did not say how to get one: %s %s", stdout, stderr)
	}
	if code, _, stderr := exercise(t, "e2e:install-browsers", "--with-deps"); code == 0 || !strings.Contains(stderr, "does not require") {
		t.Errorf("an argument for an installer nobody requires was accepted: %s", stderr)
	}
}

// TestInstallBrowsersDelegatesToThePlaywrightModule writes a project that
// requires the driver module, replaced by a directory whose installer prints
// the arguments it was handed, and reads them back.
func TestInstallBrowsersDelegatesToThePlaywrightModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	module := t.TempDir()
	writeFile(t, filepath.Join(module, "go.mod"), "module "+playwrightModule+"\n\ngo 1.26\n")
	writeFile(t, filepath.Join(module, "install-browsers", "main.go"),
		"package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() { fmt.Println(\"installer-ran\", os.Args[1:]) }\n")

	root := probeProjectRunning(t, "package main\n\nimport _ \""+playwrightModule+"/install-browsers\"\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/probe\n\ngo 1.26\n\nrequire "+playwrightModule+
		" v0.1.0\n\nreplace "+playwrightModule+" => "+module+"\n")

	code, stdout, stderr := exercise(t, "e2e:install-browsers", "--with-deps", "firefox")
	if code != 0 {
		t.Fatalf("e2e:install-browsers exited %d: %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "installer-ran [--with-deps firefox]") {
		t.Errorf("the module's installer did not run with the arguments: %s %s", stdout, stderr)
	}
}
