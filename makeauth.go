package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeAuth publishes the sign-in screens into the project.
//
// It is the starter kit, and it follows the Breeze contract: publish and get out
// of the way. Nothing it writes is a runtime dependency, and every file is the
// project's to edit from the moment it lands.
func makeAuth(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:auth", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:auth: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	files, err := gen.GenerateAuth(gen.Module{Name: "authui", ModulePath: modulePath})
	if err != nil {
		return fmt.Errorf("make:auth: %w", err)
	}

	if *dryRun {
		for _, f := range files {
			fmt.Fprintf(stdout, "%s (%d bytes)\n", f.Path, len(f.Content))
		}
		return nil
	}

	// The layout is replaced, not skipped.
	//
	// `php artisan ui bootstrap --auth` overwrites layouts/app.blade.php for the
	// same reason: the nine screens share one layout and render with its data
	// type, so a layout from somewhere else leaves them referencing fields that
	// do not exist. Skipping it produced exactly that -- nine views compiled
	// against a struct the skeleton had never heard of.
	layout := filepath.Join("resources", "views", "layouts", "app.kyse.go")
	var replace, keep []gen.File
	for _, f := range files {
		if f.Path == layout {
			replace = append(replace, f)
			continue
		}
		keep = append(keep, f)
	}
	if _, _, err := gen.Write(root, replace, true); err != nil {
		return err
	}

	written, skipped, err := gen.Write(root, keep, *force)
	if err != nil {
		return err
	}

	for _, p := range written {
		fmt.Fprintln(stdout, "created", p)
	}

	// The views are compiled right away.
	//
	// Without it the project does not build: `resources/views` holds only
	// `.kyse.go` sources, every one of them excluded by the build tag, so the
	// package the controller imports has no Go files at all. The error Go gives
	// -- "build constraints exclude all Go files" -- says nothing about the
	// command the reader has to run.
	if err := compileViews(root, stdout); err != nil {
		return fmt.Errorf("compiling the views: %w", err)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stderr, "\n%d file(s) already existed and were left alone:\n", len(skipped))
		for _, p := range skipped {
			fmt.Fprintln(stderr, "  ", p)
		}
		fmt.Fprintln(stderr, "\nrerun with --force to regenerate them; whatever sits between the")
		fmt.Fprintln(stderr, "arandu:begin custom markers is preserved.")
		if len(written) == 0 {
			return fmt.Errorf("nothing was written")
		}
	}

	fmt.Fprintf(stdout, `
the sign-in screens are yours now, %d files.

They need the view layer, and the templates need their generated Go:

    go get github.com/arandu-io/porang
    aru view:build

Then, in bootstrap/app.go, register the view layer and replace auth.New with
authui.New:

    porang.NewModule(),
    authui.New(authService, sessions, csrf, auth.FixedTenant(tenantID)),

The porang module is what serves the stylesheet and the scripts this page asks
for. Without it the page renders, unstyled, with no HTMX -- and the only sign is
three 404s in the browser console.

Both answer /auth/login, so register one of them. The framework's has the
minimum markup that exists so authentication could be tested at all; this one
has a page.
`, len(written))
	return nil
}
