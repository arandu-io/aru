package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/fonts"
)

// Browsing the catalogue, so that adding a font does not require already knowing
// the name of one.
//
// There are close to two thousand families. `font:add` alone is a command you
// cannot use without a browser open beside it, which defeats the point of the
// font being installable from the terminal.

func fontSearch(args []string, stdout, stderr io.Writer) error {
	var query, category string
	var variableOnly bool
	limit := 25

	var words []string
	for i := 0; i < len(args); i++ {
		switch flagName(args[i]) {
		case "--category", "-c":
			category, i = flagValue(args, i)
		case "--limit", "-n":
			var raw string
			raw, i = flagValue(args, i)
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		case "--variable", "-v":
			if err := noValue(args[i]); err != nil {
				return err
			}
			variableOnly = true
		case "--all":
			if err := noValue(args[i]); err != nil {
				return err
			}
			limit = 0
		default:
			if strings.HasPrefix(args[i], "-") {
				return unknownFlag("font:search", args[i])
			}
			words = append(words, args[i])
		}
	}
	query = strings.Join(words, " ")

	if category != "" && !known(category) {
		return fmt.Errorf("%q is not a category, want one of: %s",
			category, strings.Join(fonts.Categories, ", "))
	}

	all, err := fonts.Catalogue(nil)
	if err != nil {
		return err
	}
	found := fonts.Search(all, query, category, variableOnly)

	if len(found) == 0 {
		fmt.Fprintf(stdout, "nothing matches %q", query)
		if category != "" {
			fmt.Fprintf(stdout, " in %s", category)
		}
		fmt.Fprintln(stdout)
		if near := fonts.Nearest(all, query, 6); len(near) > 0 {
			fmt.Fprintf(stdout, "  did you mean: %s\n", strings.Join(near, ", "))
		}
		return nil
	}

	shown := found
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FAMILY\tCATEGORY\tWEIGHTS\tSCRIPTS\tLICENCE")
	for _, e := range shown {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			e.Family, strings.ToLower(e.Category), weights(e),
			strconv.Itoa(len(e.Subsets)), licence(e))
	}
	_ = w.Flush()

	// What was NOT shown, said out loud. A list silently cut at twenty-five
	// reads as the whole answer, and the family somebody wanted is at
	// twenty-six.
	if len(shown) < len(found) {
		fmt.Fprintf(stderr, "\n%d of %d shown -- --limit 50, or --all\n", len(shown), len(found))
	}
	fmt.Fprintf(stdout, "\n    aru font:info %q\n", shown[0].Family)
	fmt.Fprintf(stdout, "    aru font:add %q --as display\n", shown[0].Family)
	return nil
}

func fontInfo(args []string, stdout, stderr io.Writer) error {
	family := strings.Join(args, " ")
	if family == "" {
		return fmt.Errorf(`aru font:info "Young Serif"`)
	}

	all, err := fonts.Catalogue(nil)
	if err != nil {
		return err
	}
	e, ok := fonts.Find(all, family)
	if !ok {
		msg := fmt.Sprintf("no family called %q", family)
		if near := fonts.Nearest(all, family, 6); len(near) > 0 {
			msg += "\n\n  did you mean: " + strings.Join(near, ", ")
		}
		return fmt.Errorf("%s", msg)
	}

	fmt.Fprintf(stdout, "%s\n", e.Family)
	fmt.Fprintf(stdout, "  category   %s\n", strings.ToLower(e.Category))
	fmt.Fprintf(stdout, "  weights    %s\n", weights(e))
	if e.Variable() {
		fmt.Fprintf(stdout, "  axes       %s\n", strings.Join(e.Axes, ", "))
	}
	fmt.Fprintf(stdout, "  scripts    %s\n", strings.Join(e.Subsets, ", "))
	if len(e.Designers) > 0 {
		fmt.Fprintf(stdout, "  drawn by   %s\n", strings.Join(e.Designers, ", "))
	}
	fmt.Fprintf(stdout, "  licence    %s\n", licence(e))

	// The published size is the WHOLE family: every weight, every script,
	// uncompressed. Printing it beside "what this costs" would be printing two
	// numbers that differ by an order of magnitude under one heading.
	fmt.Fprintf(stdout, "\n  %.0f KB is the whole family, every weight and script.\n", float64(e.Size)/1024)
	fmt.Fprintln(stdout, "  One weight of latin is a fraction of that -- `aru font:add` prints what it wrote.")

	fmt.Fprintf(stdout, "\n    aru font:add %q --as display", e.Family)
	if e.Variable() {
		fmt.Fprintf(stdout, " --weight %s", e.WeightRange())
	}
	fmt.Fprintln(stdout)

	if !e.OpenSource {
		fmt.Fprintln(stderr, "\n  this family is not open source: read its terms before redistributing a binary with it in")
	}
	return nil
}

func weights(e fonts.Entry) string {
	if e.Variable() {
		return e.WeightRange() + " (variable)"
	}
	return e.WeightRange()
}

func licence(e fonts.Entry) string {
	if e.OpenSource {
		return "open source"
	}
	return "see terms"
}

func known(category string) bool {
	for _, c := range fonts.Categories {
		if strings.EqualFold(strings.ReplaceAll(c, "-", ""),
			strings.ReplaceAll(strings.ToLower(category), "-", "")) {
			return true
		}
	}
	return false
}
