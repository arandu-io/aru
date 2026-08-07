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

	// The layout and the two pages that share it are replaced, not skipped.
	//
	// In kyse a page that extends a layout renders with the layout's type, so
	// the layout and everything under it are one unit. Replacing the layout
	// alone left the skeleton's home and welcome passing HomeData to a layout
	// now typed AuthPage: both pages answered with the error page, and -- until
	// the renderer started buffering -- answered it with a 200.
	//
	// `php artisan ui bootstrap --auth` overwrites home.blade.php for the same
	// reason. Everything else in the kit is new files, so --force still governs
	// them.
	owned := map[string]bool{
		filepath.Join("resources", "views", "layouts", "app.kyse.go"): true,
		filepath.Join("resources", "views", "home.kyse.go"):           true,
		filepath.Join("resources", "views", "welcome.kyse.go"):        true,
		// And the controller that renders home, for the same reason: it hands
		// over the layout's type, so it changes when the layout does.
		filepath.Join("app", "Http", "Controllers", "HomeController.go"): true,
	}
	var replace, keep []gen.File
	for _, f := range files {
		if owned[f.Path] {
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
the sign-in screens are yours now, %d files, already compiled.

layouts/app, home and welcome were replaced, not skipped: a page renders with
its layout's type, so the layout and what extends it are one unit. Everything
else was left alone if it already existed.

One line in bootstrap/app.go, to answer /auth/login with these screens instead
of the framework's:

    routes.Deps{
        Login: controllers.NewLoginController(authService, sessions, csrf),
    }

Both answer /auth/login, so register one. The framework's has the minimum markup
that exists so authentication could be tested at all; this one has a page.
`, len(written))
	return nil
}
