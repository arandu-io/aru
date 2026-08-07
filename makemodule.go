package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arandu-io/aru/internal/gen"
)

// makeModule generates a module: entity, policy, repository, service, request,
// routes, handlers and tests.
//
// It calls no model. The same flags produce the same bytes, which is what makes
// the golden files in internal/gen a real test -- and what makes regeneration
// safe enough that people actually do it.
func makeModule(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:module", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fields := fs.String("fields", "", `the fields, as "name:type" separated by commas. Suffix ! for required, u for unique`)
	tenant := fs.Bool("tenant", false, "scope every query by the tenant in the Grant")
	force := fs.Bool("force", false, "overwrite existing files, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	// The name comes first and the flags after it -- which is how everyone types
	// it, and what the flag package does not support: it stops parsing at the
	// first positional argument. So the name is taken off the front by hand.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:module: %w", err)
	}
	if name == "" {
		if fs.NArg() == 0 {
			return fmt.Errorf("usage: aru make:module <name> --fields %q\n%s",
				"title:string!,amount:money", gen.TypeList())
		}
		name = fs.Arg(0)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	parsed, err := gen.ParseFields(*fields)
	if err != nil {
		return fmt.Errorf("make:module: %w", err)
	}

	spec := gen.Module{
		Name:       name,
		Fields:     parsed,
		Tenant:     *tenant,
		ModulePath: modulePath,
		Date:       time.Now().UTC().Format("2006_01_02"),
	}

	files, err := gen.Generate(spec)
	if err != nil {
		return fmt.Errorf("make:module: %w", err)
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

	// The tone is in docs/04-marca.md: say what happened and what is required
	// next, without congratulating anyone.
	//
	// Four steps, and none of them is optional: the code is written, the wiring
	// is not. A generator that edited routes/web.go and bootstrap/app.go behind
	// your back would be a generator you cannot read the output of -- the whole
	// point of explicit wiring is that the file says what the application is.
	fmt.Fprint(stdout, wiring(spec, len(written)))
	return nil
}

// wiring is what to do with the files that were just written.
//
// It is a function so it can be tested: an instruction that does not compile is
// worse than no instruction, because it is followed.
func wiring(m gen.Module, count int) string {
	return fmt.Sprintf(`
%s created, %d files, in the Laravel tree.

The policy denies every action. Open what this module needs in
app/Policies/%s.go, inside the custom block, and nothing else -- that is what
makes the default safe.

Then three lines, by hand, because the wiring is meant to be readable:

  routes/web.go -- the field, and the routes inside the custom block

      %s *controllers.%s

      r.Resource(%q, d.%s)

  bootstrap/app.go -- in the routes.Deps literal

      %s: controllers.New%s(
          services.New%s(repositories.New%s(db)), sessions, csrf),

  database/migrations/migrations.go -- inside the custom block of All()

      %s,

Then:

    aru view:build
    aru migrate
`,
		m.Name, count,
		m.PolicyType(),
		m.Entity(), m.Controller(),
		m.Resource(), m.Entity(),
		m.Entity(), m.Controller(),
		m.ServiceType(), m.RepositoryType(),
		m.MigrationVar())
}

// readModulePath reads the module path from the project's go.mod, because the
// generated test imports the module by path.
func readModulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after), nil
		}
	}
	return "", fmt.Errorf("go.mod has no module line")
}
