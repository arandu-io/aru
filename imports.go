package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
//
// What the rewrite cannot reach is printed after it: a string literal that
// spells a moved name the old way. The project compiles either way, and a test
// that reads the source and matches a call by that string stops matching, so
// each one is named with its line rather than rewritten -- the same words in
// a message or a document are the author's to change.
func fixImports(root string, c *catalog.Catalog, apply bool, stdout io.Writer) error {
	var changed []string
	moved := map[string]map[string]bool{} // old spelling: the spellings it became
	err := walkSources(root, func(path, rel string, d fs.DirEntry) error {
		before, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var after []byte
		var touched bool
		if strings.HasSuffix(path, ".kyse.go") {
			text, ok := c.RewriteView(string(before))
			after, touched = []byte(text), ok
		} else {
			var renames []catalog.Rename
			after, renames, err = c.Rewrite(path, before)
			if err != nil {
				// A file that does not parse is the compiler's report, and
				// rewriting around it would hide which file it is.
				return fmt.Errorf("imports:catalog: %s: %w", rel, err)
			}
			touched = len(renames) > 0
			for _, r := range renames {
				if r.From == r.To {
					continue
				}
				if moved[r.From] == nil {
					moved[r.From] = map[string]bool{}
				}
				moved[r.From][r.To] = true
			}
		}
		if !touched {
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
		return nil
	case apply:
		fmt.Fprintf(stdout, "%d file(s) rewritten\n", len(changed))
	default:
		fmt.Fprintf(stdout, "%d file(s) would change. Run with --fix --apply to write them.\n", len(changed))
	}
	return reportSpellings(root, moved, stdout)
}

// walkSources calls visit for every Go source and view of the module at root,
// in lexical order, with its path relative to root in slash form. It skips
// what is not the project's own source: .git, vendor, node_modules, testdata,
// bin, and the Go view:build writes under storage/framework/views.
func walkSources(root string, visit func(path, rel string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		return visit(path, rel, d)
	})
}

// reportSpellings prints every string literal in the Go sources at root that
// spells one of the old names in moved as a qualified name of its own -- not
// the tail of a longer selector -- with the spelling it became.
func reportSpellings(root string, moved map[string]map[string]bool, stdout io.Writer) error {
	if len(moved) == 0 {
		return nil
	}
	olds := make([]string, 0, len(moved))
	for old := range moved {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	patterns := make([]*regexp.Regexp, len(olds))
	for i, old := range olds {
		patterns[i] = regexp.MustCompile(`(^|[^\w.])` + regexp.QuoteMeta(old) + `\b`)
	}

	var lines []string
	err := walkSources(root, func(path, rel string, _ fs.DirEntry) error {
		if strings.HasSuffix(path, ".kyse.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for i, re := range patterns {
				if !re.MatchString(text) {
					continue
				}
				spelled := make([]string, 0, len(moved[olds[i]]))
				for to := range moved[olds[i]] {
					spelled = append(spelled, to)
				}
				sort.Strings(spelled)
				lines = append(lines, fmt.Sprintf("  %s:%d: %q is %s now", rel, fset.Position(lit.Pos()).Line, olds[i], strings.Join(spelled, " or ")))
			}
			return true
		})
		return nil
	})
	if err != nil || len(lines) == 0 {
		return err
	}
	fmt.Fprintf(stdout, "\n%d string(s) spell a moved name the old way. The project compiles either way; a test that matches source by one of them stops matching:\n", len(lines))
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}
