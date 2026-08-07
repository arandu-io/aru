package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"

	"github.com/arandu-io/aru/internal/gen"
)

// makeRequest writes one request.
//
// It is `php artisan make:request`, minus authorize(). A FormRequest is what the
// developer being migrated copies whole, and it is where the thesis of this
// product is easiest to break: authorization has one home, and it is the Policy.
func makeRequest(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:request", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fields := fs.String("fields", "", `the fields, as "name:type" separated by commas. Suffix ! for required, u for unique`)
	force := fs.Bool("force", false, "overwrite an existing request, preserving the custom blocks")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:request: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:request <Name> [--fields \"reference:string!,total:money!\"] [--force]")
	}
	if err := checkFlatTree("make:request", name); err != nil {
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

	// No automatic suffix, unlike make:controller. Controller is a universal
	// suffix in Laravel and make:module already emits it; request names vary --
	// StorePostRequest, StorePost, PostStoreRequest -- and make:module emits
	// StoreInvoice, with none. Forcing one would put two conventions in one
	// package. The name is the developer's.
	stub := gen.Stub{Type: gen.Exported(name), ModulePath: modulePath}

	if *fields != "" {
		parsed, err := gen.ParseFields(*fields)
		if err != nil {
			return fmt.Errorf("make:request: %w", err)
		}
		stub.Fields = parsed
	}

	// The collision is checked by declared type, not by file path: make:module
	// packs StoreInvoice and UpdateInvoice into InvoiceRequest.go, so a check on
	// the file name would miss it and the real error would be a "redeclared in
	// this block" three directories away.
	if !*force {
		if where, taken := requestTypeAlreadyDeclared(filepath.Join(root, "app", "Http", "Requests"), stub.Type); taken {
			return fmt.Errorf("make:request: app/Http/Requests already declares %s, in %s", stub.Type, where)
		}
	}

	files, err := gen.GenerateRequest(stub)
	if err != nil {
		return fmt.Errorf("make:request: %w", err)
	}
	if err := emit("make:request", root, files, *force, false, stdout); err != nil {
		return err
	}

	fmt.Fprint(stdout, usageRequest(stub))
	return nil
}

// requestTypeAlreadyDeclared reports which file of app/Http/Requests already
// declares a type of that name.
func requestTypeAlreadyDeclared(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok && spec.Name.Name == name {
				found = true
			}
			return !found
		})
		if found {
			return e.Name(), true
		}
	}
	return "", false
}

// usageRequest is not wiring: a request is not a dependency, it is a type. The
// message exists because validate, Authorize, Grant is the only order that
// compiles, and the developer needs to see it once.
func usageRequest(s gen.Stub) string {
	return fmt.Sprintf(`
A request is not wired: it is built and it is checked.

  app/Http/Controllers -- the controller reads the form into it

      in := requests.%s{Reference: ctx.Input("reference")}

  app/Services -- the service validates it before anything else

      if errs := in.Validate(); errs.Any() {
          return models.Invoice{}, errs
      }

Validate is the only thing this type owns. Who may do it is the Policy's answer,
asked with security.Authorize after this and before the repository. That is why
there is no authorize() here.
`, s.Type)
}
