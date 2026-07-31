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
	fmt.Fprintf(stdout, `
module %s created, %d files.

The policy denies every action -- open what this module needs in
modules/%s/%s.policy.go, inside the custom block.

Then register it in cmd/app/main.go:

    %s.New(%s.NewService(%s.NewRepo(db)), subject),

and run:

    aru migrate
`, spec.Name, len(written), spec.Package(), spec.Name, spec.Package(), spec.Package(), spec.Package())
	return nil
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
