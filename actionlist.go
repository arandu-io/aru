package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/doctor"
)

// actionList prints the actions the source states.
//
// It reads the tree and never runs it, which is the whole of why it can answer.
// A catalogue assembled while the application boots would only hold the actions
// of the modules that application registered, and what asks for this list is a
// screen that hands out permissions -- including permissions of modules the
// project has not wired yet.
//
// The nearest module is the root rather than the project, because a package
// declares actions too: a module offering `invoice.approve` has to be able to
// say so from inside itself, before any application has taken it on.
func actionList(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("action:list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	module := flags.String("module", "", "print only the actions whose name begins with this module")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("action:list: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("action:list: %q is not an argument this command takes. "+
			"Write --module=%s to print one module's actions", flags.Arg(0), flags.Arg(0))
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	actions, err := doctor.Actions(root)
	if err != nil {
		return err
	}
	printActions(stdout, actions, strings.TrimSpace(*module))
	return nil
}

// printActions writes the catalogue, grouped by the module half of the name.
//
// The grouping is the name read rather than a second field: an action is
// "module.verb", so the module is already in it, and a column carrying it again
// would be a second place for it to be wrong.
func printActions(w io.Writer, actions []doctor.Action, only string) {
	byModule := map[string][]doctor.Action{}
	for _, a := range actions {
		module, _, _ := strings.Cut(a.Value, ".")
		if only != "" && module != only {
			continue
		}
		byModule[module] = append(byModule[module], a)
	}

	if len(byModule) == 0 {
		// Saying what was read, rather than only that nothing came back. An
		// empty catalogue and a command pointed at the wrong tree print the
		// same line otherwise.
		if only != "" {
			fmt.Fprintf(w, "no action is declared under %q in this module.\n", only)
			return
		}
		fmt.Fprintln(w, "no action is declared in this module.")
		fmt.Fprintln(w, "An action is a constant of type security.Action, in \"module.verb\" form:")
		fmt.Fprintln(w, "\n    const InvoiceDelete security.Action = \"invoice.delete\"")
		return
	}

	modules := make([]string, 0, len(byModule))
	for module := range byModule {
		modules = append(modules, module)
	}
	sort.Strings(modules)

	for i, module := range modules {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s\n", module)

		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, a := range byModule[module] {
			// The identifier is what a screen stores and the value is what
			// Grant.Check compares, so both are printed: an entry whose
			// constant was renamed still holds the row it granted.
			name := a.Const
			if name == "" {
				name = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s:%d", a.Value, name, a.File, a.Line)
			if a.Doc != "" {
				fmt.Fprintf(tw, "\t%s", a.Doc)
			}
			fmt.Fprintln(tw)
		}
		_ = tw.Flush()
	}
}
