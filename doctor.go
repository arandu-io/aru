package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

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
	strict := fs.Bool("strict", false, "treat warnings as errors, or with --baseline only the warnings above it")
	// One spelling, in English like every other flag, and the two values are the
	// ones arandu.mod.toml already uses. Naming the default explicitly is how a
	// pipeline says which profile it meant.
	name := fs.String("profile", string(doctor.Conventional), "check against a deployment profile: conventional or performance")
	list := fs.Bool("list", false, "print every rule with the severity it reports at, and check nothing")
	// One path for reading and writing, so the file a team writes is the file
	// CI reads; a second path flag would let the two differ without anybody
	// noticing.
	baselinePath := fs.String("baseline", "", "compare the warnings with this file, "+doctorBaselineSuggested+" by convention")
	writeBaseline := fs.Bool("write-baseline", false, "write this run's warnings into the --baseline file")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("doctor: %w", err)
	}
	// The list is answered before a project is looked for: it is a fact about
	// this binary, and a skill that documents the rules is checked against it
	// from wherever the skill is being written.
	if *list {
		return doctor.WriteList(stdout)
	}
	if *writeBaseline && *baselinePath == "" {
		return fmt.Errorf("doctor: --write-baseline writes the file --baseline names, and means nothing without it; %s at the project root is the convention", doctorBaselineSuggested)
	}
	profile, err := doctor.ParseProfile(*name)
	if err != nil {
		return fmt.Errorf("doctor: %w", err)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	// The baseline is read before the project is checked, so a missing or
	// malformed file is the whole answer rather than a line after the report.
	var baseline *doctor.Baseline
	if *baselinePath != "" && !*writeBaseline {
		b, err := doctor.ReadBaseline(*baselinePath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("doctor: there is no baseline at %s. Write it with: aru doctor --baseline=%s --write-baseline", *baselinePath, *baselinePath)
		case err != nil:
			return fmt.Errorf("doctor: baseline %w", err)
		case b.Profile != profile:
			return fmt.Errorf("doctor: %s counts the warnings of the %s profile, and this run checks %s: the profile rules differ, so the counts do not compare", *baselinePath, b.Profile, profile)
		}
		baseline = &b
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

	if *writeBaseline {
		b := doctor.NewBaseline(profile, findings)
		if err := doctor.WriteBaseline(*baselinePath, b); err != nil {
			return fmt.Errorf("doctor: %w", err)
		}
		fmt.Fprintf(stdout, "wrote %s: %d warning(s) in %d rule and file pair(s)\n", *baselinePath, warnings, len(b.Warnings))
		baseline = &b
	}

	above := 0
	if baseline != nil {
		above = reportBaseline(stdout, *baselinePath, baseline.Compare(findings))
	}

	// The profile is named on a clean run and only then. A report that says
	// nothing about which profile produced it reads as an answer about both, and
	// the performance one is the answer somebody would act on.
	on := ""
	if profile != doctor.Conventional {
		on = " on the " + string(profile) + " profile"
	}
	beyond := ""
	if baseline != nil {
		beyond = fmt.Sprintf(", %d above the baseline", above)
	}

	switch {
	case errors == 0 && warnings == 0:
		fmt.Fprintln(stdout, "no findings"+on)
		return nil
	case errors == 0 && (!*strict || (baseline != nil && above == 0)):
		fmt.Fprintf(stdout, "%d warning(s)%s, no errors\n", warnings, beyond)
		return nil
	}

	// The count is the whole summary. A tool that ends with a paragraph of
	// encouragement is a tool people stop reading.
	return fmt.Errorf("%d error(s), %d warning(s)%s", errors, warnings, beyond)
}

// doctorBaselineSuggested is the file name `aru help doctor` suggests for the
// baseline, at the project root.
const doctorBaselineSuggested = "doctor-baseline.json"

// reportBaseline prints the rule and file pairs above the baseline at path and
// the ones it can shrink to, and answers how many warnings are above it.
//
// The pairs below are printed and never fail the run: a change that fixed a
// warning is the change the baseline exists to invite, and failing it would
// teach a team to leave the warning in. Rewriting the file is how the gain is
// kept.
func reportBaseline(w io.Writer, path string, c doctor.BaselineComparison) int {
	if len(c.Above) > 0 {
		fmt.Fprintf(w, "above %s:\n", path)
		for _, d := range c.Above {
			fmt.Fprintf(w, "  %s [%s] %d, baseline %d\n", d.File, d.Rule, d.Found, d.Baseline)
		}
		fmt.Fprintln(w)
	}
	if len(c.Below) > 0 {
		fmt.Fprintf(w, "below %s, which can shrink with: aru doctor --baseline=%s --write-baseline\n", path, path)
		for _, d := range c.Below {
			fmt.Fprintf(w, "  %s [%s] %d, baseline %d\n", d.File, d.Rule, d.Found, d.Baseline)
		}
		fmt.Fprintln(w)
	}
	return c.New()
}

// doctorHelp is the body `aru doctor --help` prints, under the usage line.
//
// It carries the baseline because the baseline is the one part of the command
// whose behaviour is not the name of its flag: which warnings fail, which are
// only printed, and what keeps the file small.
const doctorHelp = `An error fails the run, always. A warning is printed and fails it only under
--strict.

--baseline names a file of the warnings the project carries today, counted by
rule and file; doctor-baseline.json at the project root is the convention.
Write it once and commit it:

    aru doctor --baseline=doctor-baseline.json --write-baseline

Then, in CI:

    aru doctor --strict --baseline=doctor-baseline.json

Every finding is still printed. The run fails on an error, and on a rule and
file pair with more warnings than the file allows, which is listed under
"above". A pair with fewer is listed under "below" and does not fail: write the
file again in the same change and the gain is kept. A line number is not part
of the key, so editing a file does not turn its old warnings into new ones;
renaming one does, and is followed by writing the file again.`
