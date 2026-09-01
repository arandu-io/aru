package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// redacted is what a secret prints as. The word rather than a row of asterisks:
// asterisks read as a value whose length is being shown.
const redacted = "[redacted]"

// about prints the inventory of what the application has wired: its name,
// environment and URL, the driver chosen for each component, the registered
// modules, and the version.
//
// The application is the only thing that knows any of it. The drivers are
// typed structs compiled into the project and the module list is built by
// explicit registration, so a separately compiled CLI cannot read either: this
// command runs the project's own binary and renders what it answers.
//
// The arguments are read before the project is compiled, so a mistyped flag is
// answered in the time it takes to type it rather than after a build.
func about(args []string, stdout, stderr io.Writer) error {
	only, err := aboutSection(args, stderr)
	if err != nil {
		return err
	}

	// The project binary is handed no arguments at all, and --only selects from
	// what it answered. A subcommand that took a flag would have to refuse the
	// ones it did not recognise, and it cannot: the flag was typed here, so this
	// is where it is understood or rejected.
	var payload bytes.Buffer
	if err := delegate("about")(nil, &payload, stderr); err != nil {
		return err
	}
	return printAbout(stdout, payload.Bytes(), only)
}

// aboutSection reads the arguments and answers which section was asked for, or
// the empty string for the whole report.
//
// Anything it does not understand is refused with a message. Ignoring an
// argument is the failure worth avoiding here: an inventory silently narrower
// -- or silently wider -- than what was asked for is one nobody can quote.
func aboutSection(args []string, stderr io.Writer) (string, error) {
	flags := flag.NewFlagSet("about", flag.ContinueOnError)
	flags.SetOutput(stderr)
	only := flags.String("only", "", "restrict the report to one section")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("about: %w", err)
	}

	if flags.NArg() > 0 {
		return "", fmt.Errorf("about: %q is not an argument this command takes. "+
			"Write --only=%s to restrict the report to one section", flags.Arg(0), flags.Arg(0))
	}

	// --only with nothing after it asked for a section and named none. Read as
	// "no filter" it would print the whole report, which is the one answer that
	// cannot be told apart from the command working.
	given := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "only" {
			given = true
		}
	})
	name := strings.TrimSpace(*only)
	if given && name == "" {
		return "", errors.New("about: --only was given no section name")
	}
	return name, nil
}

// aboutReport is what the project binary answers with: the sections, in the
// order they are meant to be read.
//
// The shape is declared here rather than shared with the framework, for the
// same reason the trace payloads are: the CLI and the application are separate
// programs built at different times, and a shared type would make their
// versions have to match.
type aboutReport struct {
	Sections []aboutSectionPayload `json:"Sections"`
}

// aboutSectionPayload is one group of the report, with the label --only takes.
type aboutSectionPayload struct {
	Name    string       `json:"Name"`
	Entries []aboutEntry `json:"Entries"`
}

// aboutEntry is one line of a section: a label, and what is wired behind it.
//
// Secret marks a value that must not reach the terminal. It travels in the
// payload because the application is what knows which of its settings are
// credentials; the CLI would otherwise have to guess for every one of them.
type aboutEntry struct {
	Name   string `json:"Name"`
	Value  string `json:"Value"`
	Secret bool   `json:"Secret"`
}

// printAbout renders the report, restricted to only when it is not empty.
func printAbout(w io.Writer, payload []byte, only string) error {
	var report aboutReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return fmt.Errorf("about: the application answered something unexpected: %w", err)
	}
	if len(report.Sections) == 0 {
		return errors.New("about: the application reported no sections")
	}

	sections := report.Sections
	if only != "" {
		sections = nil
		for _, s := range report.Sections {
			if sameSection(s.Name, only) {
				sections = append(sections, s)
			}
		}
		// The names come from the report rather than from a list kept here, so
		// a section the application grows later is offered on the day it exists.
		if len(sections) == 0 {
			names := make([]string, 0, len(report.Sections))
			for _, s := range report.Sections {
				names = append(names, s.Name)
			}
			return fmt.Errorf("about: this application has no section called %q. It reports %s",
				only, strings.Join(names, ", "))
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		fmt.Fprintf(tw, "%s\n", s.Name)
		for _, e := range s.Entries {
			fmt.Fprintf(tw, "  %s\t%s\n", e.Name, aboutValue(e))
		}
	}
	return tw.Flush()
}

// aboutValue is what one entry prints as.
//
// A secret never reaches the terminal. This report is what somebody pastes into
// a bug report, and an application key pasted anywhere is a key that has to be
// rotated -- which invalidates every session and every value encrypted with it.
// Whether the key is set is still said, because a missing one and a present one
// fail in completely different ways and neither answer leaks anything.
//
// The shape check is not a second rule, it is the floor under the first: the
// payload declares what is secret, and an application that forgets to declare
// the one credential every project has still cannot print it here.
func aboutValue(e aboutEntry) string {
	if strings.TrimSpace(e.Value) == "" {
		if e.Secret {
			return "not set"
		}
		return e.Value
	}
	if e.Secret || looksLikeAppKey(e.Value) {
		return redacted
	}
	return e.Value
}

// looksLikeAppKey reports whether a value has the shape of an application key:
// the base64 prefix, and thirty-two bytes once decoded. Nothing else in the
// configuration is written that way.
func looksLikeAppKey(value string) bool {
	encoded, ok := strings.CutPrefix(strings.TrimSpace(value), "base64:")
	if !ok {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(key) == 32
}

// sameSection compares what was typed against a section name, ignoring case and
// the spaces a name can carry, so --only=drivers reaches "Drivers" and
// --only=queue reaches a "Queue" section without anybody quoting a shell word.
func sameSection(name, typed string) bool {
	fold := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, " ", "")) }
	return fold(name) == fold(typed)
}
