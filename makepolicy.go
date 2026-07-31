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

	dir := filepath.Join(root, "modules", strings.ReplaceAll(name, "_", ""))
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("module %s does not exist in modules/ -- create it with `aru make:module`", name)
	}

	// The policy is generated from the same specification as the rest, with the
	// tenant inferred from what the module already does. Asking again for
	// something the code already answers is how two sources of truth start.
	spec := gen.Module{
		Name:       name,
		Fields:     []gen.Field{{Name: "placeholder", Type: gen.TypeString}},
		Tenant:     moduleUsesTenant(dir),
		ModulePath: modulePath,
	}

	files, err := gen.Generate(spec)
	if err != nil {
		return fmt.Errorf("make:policy: %w", err)
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".policy.go") {
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

// moduleUsesTenant reports whether the module already scopes by tenant, so the
// generated policy matches what the repository does.
func moduleUsesTenant(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "data.Tenant(g)") || strings.Contains(string(b), "TenantID") {
			return true
		}
	}
	return false
}
