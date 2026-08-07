package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/arandu-io/aru/internal/toolchain"
)

// stylesheetSource is where a project keeps the stylesheet it compiles, in the
// place a Laravel developer looks for it first.
const stylesheetSource = "resources/css/app.css"

// stylesheetOutput sits next to the code that embeds it, so `go:embed` finds it
// and the deploy stays one binary -- no asset publishing step, no CDN.
const stylesheetOutput = "assets/app.css"

// viewBuild turns every `.kyse.go` into Go and, when the project has its own
// stylesheet, compiles the CSS.
//
// The view compiler is kyse, which is part of this CLI (ADR 0020); the CSS is
// the Tailwind standalone binary aru downloads and verifies (doc 14). Neither is
// Node, and neither is optional at deploy time: what they produce is committed,
// so the server only ever runs `go build`.
func viewBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("view:build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	watch := flags.Bool("watch", false, "rebuild when a file changes")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("view:build: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	return buildViews(root, *watch, stdout, stderr)
}

func buildViews(root string, watch bool, stdout, stderr io.Writer) error {
	pins, err := toolchain.ReadPins(root)
	if err != nil {
		return err
	}
	// On stderr, so `aru view:build > log` keeps the warning where a person sees
	// it and out of what the command produced.
	pins.Warn(stderr)

	// kyse is part of this CLI, not a binary it downloads. One fewer thing to
	// pin, verify and cache -- and the view compiler moving in lockstep with the
	// generator that writes views is what keeps them from drifting.
	if err := compileViews(root, stdout); err != nil {
		return err
	}

	if !hasStylesheet(root) {
		// A project with no stylesheet of its own has nothing to compile: the
		// base one is embedded in the framework's view package and served from
		// there (ADR 0021), so the views still render styled.
		return nil
	}

	tailwind, err := toolchain.Tailwind(pins.Tailwind).Ensure(stdout)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(stylesheetOutput)), 0o755); err != nil {
		return err
	}
	args := []string{"--input", stylesheetSource, "--output", stylesheetOutput, "--minify"}
	if watch {
		args = append(args, "--watch")
	}
	if err := runTool(root, tailwind, args, stdout, stderr); err != nil {
		return fmt.Errorf("tailwindcss: %w", err)
	}

	fmt.Fprintf(stdout, "wrote %s\n", stylesheetOutput)
	return nil
}

func runTool(dir, binary string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func hasStylesheet(root string) bool {
	_, err := os.Stat(filepath.Join(root, stylesheetSource))
	return !errors.Is(err, fs.ErrNotExist)
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "testdata":
		return true
	}
	return false
}
