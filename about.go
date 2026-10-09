package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/doctor"
	"github.com/arandu-io/aru/internal/gomod"
)

// redacted is what a secret prints as. The word rather than a row of asterisks:
// asterisks read as a value whose length is being shown.
const redacted = "[redacted]"

// about prints the inventory of what the application is configured with: its
// name, environment and URL, the versions it builds against, the driver each
// setting names, and the modules and components its source wires in.
//
// It reads the project and never runs it. The answer used to come from the
// project's binary, through a subcommand no project's dispatch has ever
// answered -- the skeleton's switch has no case for it -- so the command failed
// in every application that exists. Teaching the skeleton would reach only the
// projects generated after the lesson: bootstrap/console.go belongs to the
// project, and nothing here edits it. Everything the report holds is on disk
// instead: go.mod, the environment and .env read the way the boot reads them,
// and the registrations the doctor's project graph already finds.
//
// What that costs is said in the report rather than hidden. A setting is shown
// as the environment and .env give it; a default the project's own config
// applies to an unset variable is not something a reader of the tree can know,
// and the report says "not set" rather than guessing it.
func about(args []string, stdout, stderr io.Writer) error {
	only, err := aboutSection(args, stderr)
	if err != nil {
		return err
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	report, err := readAbout(root)
	if err != nil {
		return err
	}
	return printAbout(stdout, report, only)
}

// The variables each section reads, with the label they are shown under. The names are the ones the boot reads: APP_* by the framework's
// configuration, DATABASE_URL by its database loader, and the driver settings
// by the skeleton's config/ files, under the spelling .env.example documents.
var (
	aboutEnvironment = []aboutSetting{
		{"Application Name", "APP_NAME"},
		{"Environment", "APP_ENV"},
		{"URL", "APP_URL"},
		{"Application Key", "APP_KEY"},
	}
	aboutDrivers = []aboutSetting{
		{"Database", "DATABASE_URL"},
		{"Cache", "CACHE_STORE"},
		{"Session", "SESSION_DRIVER"},
		{"Queue", "QUEUE_CONNECTION"},
		{"Mail", "MAIL_MAILER"},
		{"Filesystem", "FILESYSTEM_DISK"},
	}
	// aboutModules are the modules whose version is worth a line: the three
	// the skeleton requires and every project builds on.
	aboutModules = []aboutSetting{
		{"framework", "github.com/arandu-io/framework"},
		{"hesape", "github.com/arandu-io/hesape"},
		{"kyse", "github.com/arandu-io/kyse"},
	}
)

type aboutSetting struct{ label, name string }

// notSet is what an unset setting prints as. It says what is known -- nothing
// sets it -- and where the answer is instead.
const notSet = "not set: the application's default applies"

// readAbout builds the report for the project at root.
func readAbout(root string) (aboutReport, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return aboutReport{}, fmt.Errorf("about: %w", err)
	}
	mod := gomod.Parse(string(body))
	dotenv := doctor.EnvFile(filepath.Join(root, ".env"))

	// The environment first and .env for what it leaves undefined, which is
	// the precedence the boot applies: a variable exported as the empty string
	// is a decision, and the file does not overrule it.
	setting := func(name string) (string, bool) {
		if value, defined := os.LookupEnv(name); defined {
			return value, true
		}
		value, defined := dotenv[name]
		return value, defined
	}

	environment := aboutSectionPayload{Name: "Environment"}
	for _, s := range aboutEnvironment {
		value, defined := setting(s.name)
		entry := aboutEntry{Name: s.label, Value: value, Secret: s.name == "APP_KEY"}
		if !defined && !entry.Secret {
			entry.Value = notSet
		}
		environment.Entries = append(environment.Entries, entry)
	}

	versions := aboutSectionPayload{Name: "Versions", Entries: []aboutEntry{
		{Name: "Module", Value: mod.Path},
		{Name: "Go", Value: mod.Go},
		{Name: "aru", Value: version},
	}}
	for _, m := range aboutModules {
		v, required := mod.Versions[m.name]
		if !required {
			continue
		}
		if dir, replaced := mod.Replaced[m.name]; replaced {
			v = "replaced by " + dir
		} else if pinned, ok := mod.Pinned(m.name); ok {
			v = pinned
		}
		versions.Entries = append(versions.Entries, aboutEntry{Name: m.label, Value: v})
	}

	drivers := aboutSectionPayload{Name: "Drivers"}
	for _, s := range aboutDrivers {
		value, defined := setting(s.name)
		switch {
		case !defined || strings.TrimSpace(value) == "":
			value = notSet
		case s.name == "DATABASE_URL":
			value = databaseShown(value)
		}
		drivers.Entries = append(drivers.Entries, aboutEntry{Name: s.label, Value: value})
	}

	report := aboutReport{Sections: []aboutSectionPayload{environment, versions, drivers}}

	analysis, err := doctor.Analyze(root, doctor.Conventional)
	if err != nil {
		return aboutReport{}, fmt.Errorf("about: %w", err)
	}
	modules := aboutSectionPayload{Name: "Modules"}
	components := aboutSectionPayload{Name: "Components"}
	for _, node := range analysis.Graph.Nodes {
		switch node.Kind {
		case "community-module":
			// "Registered in bootstrap" or "Required in go.mod", then where.
			where := node.File + ":" + strconv.Itoa(node.Line)
			if detail := node.Detail; detail != "" {
				where = strings.ToLower(detail[:1]) + detail[1:] + ", " + where
			}
			modules.Entries = append(modules.Entries, aboutEntry{Name: node.Label, Value: where})
		case "native-capability":
			components.Entries = append(components.Entries, aboutEntry{Name: node.Label, Value: node.Detail})
		}
	}
	for _, section := range []aboutSectionPayload{modules, components} {
		sort.Slice(section.Entries, func(i, j int) bool { return section.Entries[i].Name < section.Entries[j].Name })
		if len(section.Entries) > 0 {
			report.Sections = append(report.Sections, section)
		}
	}
	return report, nil
}

// databaseShown is what a DATABASE_URL prints as: the engine it selects, and
// never the URL, which carries the password. A scheme no connector speaks is
// named as it was written, because the boot refuses it in those words.
func databaseShown(raw string) string {
	if dialect := doctor.DatabaseDialect(raw); dialect != "" {
		return dialect
	}
	scheme, _, found := strings.Cut(strings.TrimSpace(raw), "://")
	if !found || scheme == "" {
		return "a URL with no scheme"
	}
	return scheme + ", which no connector speaks"
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

// aboutReport is the inventory, in the order it is meant to be read.
type aboutReport struct {
	Sections []aboutSectionPayload
}

// aboutSectionPayload is one group of the report, with the label --only takes.
type aboutSectionPayload struct {
	Name    string
	Entries []aboutEntry
}

// aboutEntry is one line of a section: a label, and what is wired behind it.
//
// Secret marks a value that must not reach the terminal.
type aboutEntry struct {
	Name   string
	Value  string
	Secret bool
}

// printAbout renders the report, restricted to only when it is not empty.
func printAbout(w io.Writer, report aboutReport, only string) error {
	if len(report.Sections) == 0 {
		return errors.New("about: the report has no sections")
	}

	sections := report.Sections
	if only != "" {
		sections = nil
		for _, s := range report.Sections {
			if sameSection(s.Name, only) {
				sections = append(sections, s)
			}
		}
		// The names come from the report rather than from a second list, so a
		// section left out because nothing was found in it -- Modules on a
		// project that registers none -- is not offered either.
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
// report marks what is secret, and a key that reaches a value nobody marked
// still cannot print here.
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
