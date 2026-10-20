package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/arandu-io/aru/internal/buildcache"
)

// browserTestsDir is where a project keeps the tests that drive a browser.
const browserTestsDir = "tests/Browser"

// browserTag is the build tag those tests carry. It keeps them out of the
// ordinary go test ./..., which has no browser to start.
const browserTag = "browser"

// errNoBrowserTests is the answer to --browser in a project without the
// directory the browser tests live in.
var errNoBrowserTests = errors.New("there is no " + browserTestsDir + " in this project: a browser test is a Go test " +
	"package there, each file opening with //go:build " + browserTag + ", and aru test --browser runs it")

// runTests runs the project's tests with go test, from the project root.
//
// Without --browser it is go test ./..., which the build tag keeps away from
// tests/Browser. With it, it is go test -tags browser ./tests/Browser/...,
// and nothing else: the browser suite is slow and needs a browser, so it runs
// when it is asked for. Every other argument is handed to go test as it is --
// -run, -race, -v, -count -- so this parses no flag set of its own; --browser
// is taken out wherever it stands before a bare --.
//
// The toolchain is started through the same cache and compiler flags as every
// command that runs the application, so a test after aru dev compiles nothing
// twice, and the view layer is built first when it is missing, because the
// tests import the application and the application imports its views.
func runTests(args []string, stdout, stderr io.Writer) error {
	browser, rest := takeBrowser(args)

	root, err := projectRoot()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("the go toolchain was not found in PATH, and aru needs it to run the tests")
	}

	packages := []string{"./..."}
	if browser {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(browserTestsDir))); err != nil || !info.IsDir() {
			return errNoBrowserTests
		}
		packages = []string{"-tags", browserTag, "./" + browserTestsDir + "/..."}
	}

	if err := ensureViews(root, stderr); err != nil {
		return err
	}

	cmd := buildcache.Application("test", append(packages, rest...)...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// go test has already said which test failed and why.
			return errors.New("the tests failed")
		}
		return fmt.Errorf("running go test: %w", err)
	}
	return nil
}

// takeBrowser removes --browser from the arguments and reports whether it was
// there. Scanning stops at a bare --: what follows is for go test, even a
// --browser.
func takeBrowser(args []string) (bool, []string) {
	browser := false
	rest := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--browser" || a == "-browser" {
			browser = true
			continue
		}
		rest = append(rest, a)
	}
	return browser, rest
}
