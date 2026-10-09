package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/catalog"
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
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("imports:catalog: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("imports:catalog: %q is not an argument this command takes", flags.Arg(0))
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	c, err := catalog.Fetch(root)
	if err != nil {
		return fmt.Errorf("imports:catalog: %w", err)
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
