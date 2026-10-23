package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/testlayout"
)

// listTests prints every test of the project at root, read from its sources:
// grouped by layer and package as a table, or as the inventory's JSON form
// when asJSON is set. Nothing is compiled and nothing runs, so a project whose
// build is broken is still listed, and a file the parser refuses is named on
// stderr -- or in the JSON's unreadable -- rather than failing the command.
func listTests(root string, asJSON bool, stdout, stderr io.Writer) error {
	inv, err := testlayout.List(root)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(inv)
	}

	for _, u := range inv.Unreadable {
		fmt.Fprintf(stderr, "aru test --list: %s does not parse, and its tests are not listed: %s\n", u.File, u.Error)
	}
	if len(inv.Tests) == 0 {
		fmt.Fprintln(stdout, "no tests: a test is a func TestX(t *testing.T) in a _test.go file, and aru make:test <Name> writes one")
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	subtests := 0
	layers := 0
	for _, layer := range inv.Layers {
		var tests []testlayout.Test
		for _, t := range inv.Tests {
			if t.Layer == layer.Name {
				tests = append(tests, t)
			}
		}
		if len(tests) == 0 {
			continue
		}
		layers++
		header := layer.Name + " (" + plural(len(tests), "test")
		if layer.Dir != "" {
			header += ", " + layer.Dir
		}
		if layer.Tag != "" {
			header += ", -tags " + layer.Tag
		}
		fmt.Fprintln(w, header+")")
		pkg := ""
		for _, t := range tests {
			if t.Package != pkg {
				pkg = t.Package
				fmt.Fprintln(w, "  "+pkg)
			}
			row := fmt.Sprintf("    %s\t%s:%d", t.Name, t.File, t.Line)
			if len(t.Tags) > 0 {
				row += "\t" + strings.Join(t.Tags, ",")
			}
			fmt.Fprintln(w, row)
			for _, s := range t.Subtests {
				fmt.Fprintf(w, "      %s\t%s:%d\n", s.Name, t.File, s.Line)
			}
			subtests += len(t.Subtests)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s and %s in %s\n", plural(len(inv.Tests), "test"), plural(subtests, "subtest"), plural(layers, "layer"))
	return nil
}

// plural is n followed by word, with an s unless n is one.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
