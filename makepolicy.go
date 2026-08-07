package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makePolicy writes the policy of a module that does not have one.
//
// It exists for the module written before the generator, and for the one whose
// policy was deleted -- both of which `aru doctor` reports as an entity nobody
// decided who may reach. This is the fix it points at.
func makePolicy(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:policy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing policy, preserving the custom block")

	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:policy <module>")
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// The entity is what the policy is about, and app/Models is where it lives.
	// Asking for a policy about something that does not exist is almost always a
	// typo, and finding out here beats finding out from a file nobody references.
	spec := gen.Module{
		Name:       name,
		Fields:     []gen.Field{{Name: "placeholder", Type: gen.TypeString}},
		ModulePath: modulePath,
	}
	entity := filepath.Join(root, "app", "Models", spec.Entity()+".go")
	if _, err := os.Stat(entity); err != nil {
		return fmt.Errorf("app/Models/%s.go does not exist -- create the module with `aru make:module %s`",
			spec.Entity(), name)
	}

	// The tenant is inferred from what the repository already does, rather than
	// asked for again: two sources of truth about one decision is how they drift.
	spec.Tenant = repositoryUsesTenant(filepath.Join(root, "app", "Repositories", spec.RepositoryType()+".go"))

	files, err := gen.Generate(spec)
	if err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}

	for _, f := range files {
		if filepath.Dir(f.Path) != filepath.Join("app", "Policies") {
			continue
		}
		written, skipped, err := gen.Write(root, []gen.File{f}, *force)
		if err != nil {
			return err
		}
		if len(skipped) > 0 {
			return fmt.Errorf("%s already exists; rerun with --force to regenerate it (the custom block is preserved)", f.Path)
		}
		fmt.Fprintln(stdout, "created", written[0])
		fmt.Fprintf(stdout, `
The policy denies every action. Open what this module needs inside the custom
block, and nothing else -- that is what makes the default safe.
`)
		return nil
	}
	return fmt.Errorf("make:policy: no policy was generated")
}

// repositoryUsesTenant reports whether the repository already scopes by tenant,
// so the generated policy matches what the queries do.
//
// A policy that forgot the tenant check on a repository that filters by it is
// the one combination that looks safe and is not: every query is scoped, so
// nothing leaks until somebody adds a query that is not.
func repositoryUsesTenant(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "data.Tenant(g)")
}
