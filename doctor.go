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
	// One spelling, in English like every other flag, and the two values are the
	// ones arandu.mod.toml already uses. Naming the default explicitly is how a
	// pipeline says which profile it meant.
	name := fs.String("profile", string(doctor.Conventional), "check against a deployment profile: conventional or performance")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("doctor: %w", err)
	}
	profile, err := doctor.ParseProfile(*name)
	if err != nil {
		return fmt.Errorf("doctor: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	findings, err := doctor.Run(root, profile)
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

	// The profile is named on a clean run and only then. A report that says
	// nothing about which profile produced it reads as an answer about both, and
	// the performance one is the answer somebody would act on.
	on := ""
	if profile != doctor.Conventional {
		on = " on the " + string(profile) + " profile"
	}

	switch {
	case errors == 0 && warnings == 0:
		fmt.Fprintln(stdout, "no findings"+on)
		return nil
	case errors == 0 && !*strict:
		fmt.Fprintf(stdout, "%d warning(s), no errors\n", warnings)
		return nil
	}

	// The count is the whole summary. A tool that ends with a paragraph of
	// encouragement is a tool people stop reading.
	return fmt.Errorf("%d error(s), %d warning(s)", errors, warnings)
}
