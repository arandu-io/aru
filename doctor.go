package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/doctor"
)

// runDoctor checks that the project honors the framework contracts.
//
// It is what turns the rules in the documents into something CI can enforce.
// Without it, "the architecture is mandatory" holds only where the type system
// reaches -- and the type system cannot see a policy nobody opened, a handler
// that talks to the database, or a tenant that came from a header.
func runDoctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false, "treat warnings as errors")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("doctor: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	findings, err := doctor.Run(root)
	if err != nil {
		return fmt.Errorf("doctor: %w", err)
	}

	var errors, warnings int
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
		fmt.Fprintln(stdout)
		if f.Severity == doctor.Error {
			errors++
		} else {
			warnings++
		}
	}

	switch {
	case errors == 0 && warnings == 0:
		fmt.Fprintln(stdout, "no findings")
		return nil
	case errors == 0 && !*strict:
		fmt.Fprintf(stdout, "%d warning(s), no errors\n", warnings)
		return nil
	}

	// The count is the whole summary. A tool that ends with a paragraph of
	// encouragement is a tool people stop reading.
	return fmt.Errorf("%d error(s), %d warning(s)", errors, warnings)
}
