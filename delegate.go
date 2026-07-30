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
const appPackage = "./cmd/app"

// delegate returns a command that forwards to the project's own binary.
//
// serve, migrate and routes all need the list of registered modules, and that
// list only exists inside the application: the modules are wired explicitly in
// cmd/app/main.go, with no container and no plugin loading. A CLI compiled
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

	for {
		_, modErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, appErr := os.Stat(filepath.Join(dir, "cmd", "app"))
		if modErr == nil && appErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no cmd/app found: run this from inside an Arandu project, or create one with `aru new`")
		}
		dir = parent
	}
}
