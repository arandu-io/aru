package main

import (
	"flag"
	"fmt"
	"io"

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

	written, skipped, err := gen.Write(root, files, *force)
	if err != nil {
		return err
	}

	for _, p := range written {
		fmt.Fprintln(stdout, "created", p)
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

Then, in cmd/app/main.go, replace auth.New with authui.New:

    authui.New(authService, sessions, csrf, auth.FixedTenant(tenantID)),

Both answer /auth/login, so register one of them. The framework's has the
minimum markup that exists so authentication could be tested at all; this one
has a page.
`, len(written))
	return nil
}
