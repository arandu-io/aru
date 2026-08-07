package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeMiddleware writes one middleware.
//
// It is `php artisan make:middleware`, and it is the piece that travels most
// intact in a migration: the "admins only", the "subscription is active", the
// "this tenant and no other" are all middleware in the application being ported.
func makeMiddleware(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:middleware", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing middleware, preserving the custom block")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:middleware: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:middleware <Name> [--force]")
	}
	if err := checkFlatTree("make:middleware", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// No automatic suffix: Laravel's middleware has none by convention --
	// EnsureTokenIsValid -- and inventing one here would be inventing vocabulary.
	stub := gen.Stub{Type: gen.Exported(name), ModulePath: modulePath}

	files, err := gen.GenerateMiddleware(stub)
	if err != nil {
		return fmt.Errorf("make:middleware: %w", err)
	}
	if err := emit("make:middleware", root, files, *force, false, stdout); err != nil {
		return err
	}

	fmt.Fprint(stdout, wiringMiddleware(stub))
	return nil
}

// wiringMiddleware prints both places a middleware can go, because they are
// different scopes rather than two ways to do one thing -- exactly as global and
// route middleware are in Laravel.
func wiringMiddleware(s gen.Stub) string {
	return fmt.Sprintf(`
Where it goes is what it protects, and both places are by hand. The import, in
either file:

    appmiddleware %q

  routes/web.go -- one group or one route, which is the usual case

      admin := r.Group("/admin", appmiddleware.%s())
      r.Action("GET", "/billing", d.Billing.Handle, appmiddleware.%s())

  bootstrap/app.go -- global, in the k.Use(...) pipeline, where the order of the
  list is the order of execution

      Use(..., appmiddleware.%s())

Global means every request, including /favicon.ico and the sign-in page. Pick
the group unless the answer is genuinely "all of them".
`, s.ModulePath+"/app/Http/Middleware", s.Type, s.Type, s.Type)
}
