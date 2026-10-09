package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/catalog"
	"github.com/arandu-io/aru/internal/skills"
)

// importsCatalog prints, for every exported symbol of the framework the module
// requires, the import path a project should name it by.
//
// It is read from the framework's source at the version go.mod requires, not
// from a list kept here: whether a symbol is an alias of a component's or the
// framework's own is a fact about one release, and the next one can move it.
// The nearest module is the root rather than the project, because a package
// written against the framework asks the same question before any application
// has taken it on.
//
// The source is read from where the toolchain would read it. When it is not on
// disk yet, the command asks the toolchain to download that version and reads
// it from where the download put it -- the one step here that may reach the
// network, and the reason the doctor, which never starts the toolchain, says
// nothing about imports on a machine that has not built the project.
func importsCatalog(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("imports:catalog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the catalog as JSON")
	fix := flags.Bool("fix", false, "show how each file would name the symbols a bridge only re-exports by their canonical path")
	apply := flags.Bool("apply", false, "with --fix, write the rewritten files")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("imports:catalog: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("imports:catalog: %q is not an argument this command takes", flags.Arg(0))
	}
	if *apply && !*fix {
		return fmt.Errorf("imports:catalog: --apply writes what --fix shows, and means nothing without it")
	}
	if *fix && *asJSON {
		return fmt.Errorf("imports:catalog: --fix prints a diff, not JSON; pick one")
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	c, err := catalog.Fetch(root)
	if err != nil {
		return fmt.Errorf("imports:catalog: %w", err)
	}

	if *fix {
		return fixImports(root, c, *apply, stdout)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	}
	return printCatalog(stdout, c)
}

// printCatalog writes one block per package and one line per symbol: its
// name, what it is declared as, how the catalog decided, and the path and name
// to write. A symbol whose path is the framework's says so in the same column,
// so a line read alone is the whole answer.
func printCatalog(w io.Writer, c *catalog.Catalog) error {
	version := c.Version
	if version == "" {
		version = "(no version: read from " + c.Dir + ")"
	}
	fmt.Fprintf(w, "%s %s\n", c.Module, version)

	for _, pkg := range c.Packages {
		if len(pkg.Symbols) == 0 {
			continue
		}
		fmt.Fprintln(w)
		if pkg.Bridge != "" {
			fmt.Fprintf(w, "%s (bridge to %s)\n", pkg.Path, pkg.Bridge)
		} else {
			fmt.Fprintln(w, pkg.Path)
		}
		// One table per package, so a long name in one package does not widen
		// the columns of every other.
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, s := range pkg.Symbols {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s.%s\n", s.Name, s.Kind, s.How, s.Canonical, s.CanonicalName)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// fixImports rewrites the imports of every Go source and view of the module
// at root so that a symbol a bridge only re-exports is named by its canonical
// path, and prints the diff of each file. It writes only when apply is set.
//
// The Go view:build writes under storage/framework/views is left alone: the
// next build writes it again from the .kyse.go, which is rewritten instead.
// vendor, testdata, bin and node_modules are not the project's sources.
func fixImports(root string, c *catalog.Catalog, apply bool, stdout io.Writer) error {
	var changed []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "bin":
				return filepath.SkipDir
			}
			if rel == "storage/framework/views" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		before, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var after []byte
		var moved bool
		if strings.HasSuffix(path, ".kyse.go") {
			text, ok := c.RewriteView(string(before))
			after, moved = []byte(text), ok
		} else {
			after, moved, err = c.Rewrite(path, before)
			if err != nil {
				// A file that does not parse is the compiler's report, and
				// rewriting around it would hide which file it is.
				return fmt.Errorf("imports:catalog: %s: %w", rel, err)
			}
		}
		if !moved {
			return nil
		}
		changed = append(changed, rel)
		if apply {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, after, info.Mode().Perm()); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "rewrote", rel)
			return nil
		}
		fmt.Fprint(stdout, skills.Diff(rel, before, after))
		return nil
	})
	if err != nil {
		return err
	}
	switch {
	case len(changed) == 0:
		fmt.Fprintln(stdout, "every import already names its symbols by their canonical path")
	case apply:
		fmt.Fprintf(stdout, "%d file(s) rewritten\n", len(changed))
	default:
		fmt.Fprintf(stdout, "%d file(s) would change. Run with --fix --apply to write them.\n", len(changed))
	}
	return nil
}
