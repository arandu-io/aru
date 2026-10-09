package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeClientUsage is the usage line of make:client, in the dispatch table and
// in its refusal, naming every flag the command accepts.
const makeClientUsage = `aru make:client <Vendor> [--force] [--dry-run]`

// makeClient writes the client of one external system: the typed config, the
// small interface the code calling it depends on, the client over
// hesape/http/client, the fake a test hands in its place, and the test.
//
// A client useful to more than one project is a module, not this file; this
// is for the system one application talks to.
func makeClient(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite the client, its fake and its test, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:client: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeClientUsage)
	}
	if err := checkFlatTree("make:client", name); err != nil {
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

	spec := gen.ClientSpec{Vendor: unsuffixed(name, "Client"), ModulePath: modulePath}
	files, err := gen.RenderClient(spec)
	if err != nil {
		return fmt.Errorf("make:client: %w", err)
	}
	if err := emit("make:client", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
The client is written, the wiring is not. bootstrap/app.go builds it once,
with its configuration and a request factory, and hands it to the service that
calls it -- by hand, because the wiring is meant to be readable:

      %[1]s := clients.New%[2]s(clients.%[3]s{
          BaseURL: "https://...",   // from the configuration
          Token:   "...",
      }, client.NewFactory(nil))

importing clients "%[4]s" and "github.com/arandu-io/hesape/http/client".
The service takes clients.%[5]s, never the concrete type, and a test builds it
with &clients.%[6]s{} instead. Only a service, a job or a listener calls it.
`, gen.Module{Name: gen.Normalize(spec.Vendor)}.Unexported(), spec.Type(), spec.Config(),
		spec.ClientsImport(), spec.Interface(), spec.Fake())
	return nil
}
