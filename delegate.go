package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// appPackage is where the skeleton puts the application entry point.
// appPackage is what `go build` compiles: the module root, where main.go lives.
//
// It used to be "./cmd/app". The tree mirrors Laravel now, and Laravel's entry
// point is at the root — main.go replaces both public/index.php and artisan
// (ADR 0019).
const appPackage = "."

// delegate returns a command that forwards to the project's own binary.
//
// serve, migrate and routes all need the list of registered modules, and that
// list only exists inside the application: the modules are wired explicitly in
// bootstrap/app.go, with no container and no plugin loading. A CLI compiled
// separately cannot know them, so the honest implementation is to run the
// project. This is also why those subcommands exist in the skeleton's main.
func delegate(subcommand string) func([]string, io.Writer, io.Writer) error {
	return func(args []string, stdout, stderr io.Writer) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}

		if _, err := exec.LookPath("go"); err != nil {
			return errors.New("the go toolchain was not found in PATH, and aru needs it to run the project")
		}

		full := append([]string{"run", appPackage, subcommand}, args...)
		cmd := exec.Command("go", full...)
		cmd.Dir = root
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				// The application already reported the reason on stderr; adding a
				// wrapper line here would only bury it.
				return fmt.Errorf("%s failed", subcommand)
			}
			return fmt.Errorf("running %s: %w", appPackage, err)
		}
		return nil
	}
}

// projectRoot walks up from the working directory looking for a go.mod next to
// cmd/app, so the command works from anywhere inside the project.
func projectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// An Arandu project is a Go module with main.go at the root and an
	// arandu.toml beside it. The arandu.toml is what tells it apart from any
	// other Go module -- without it, running `aru` inside an unrelated project
	// would walk up and act on it.
	for {
		_, modErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, mainErr := os.Stat(filepath.Join(dir, "main.go"))
		_, tomlErr := os.Stat(filepath.Join(dir, "arandu.toml"))
		if modErr == nil && mainErr == nil && tomlErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("this is not an Arandu project: no go.mod, main.go and arandu.toml together. " +
				"Run it from inside a project, or create one with `aru new`")
		}
		dir = parent
	}
}
