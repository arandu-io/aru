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

// viewBuild turns .templ into Go and, when the project has its own stylesheet,
// compiles the CSS.
//
// Both tools are single binaries managed by aru (doc 14). Neither is Node, and
// neither is optional at deploy time: what they produce is committed, so the
// server only ever runs `go build`.
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

	if hasTemplates(root) {
		templ, err := toolchain.Templ(pins.Templ).Ensure(stdout)
		if err != nil {
			return err
		}
		args := []string{"generate"}
		if watch {
			args = append(args, "--watch")
		}
		if err := runTool(root, templ, args, stdout, stderr); err != nil {
			return fmt.Errorf("templ: %w", err)
		}
	}

	if !hasStylesheet(root) {
		// A project that only uses porang's components has nothing to compile:
		// the stylesheet those components need is already embedded in porang.
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

// hasTemplates reports whether the project has anything for templ to do.
func hasTemplates(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		if filepath.Ext(path) == ".templ" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
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
