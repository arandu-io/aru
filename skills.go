package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/arandu-io/aru/internal/buildcache"
	"github.com/arandu-io/aru/internal/skills"
)

// skillsSync brings the project's skills in step with where they came from:
// the skeleton at the version this build pins, and every module under
// skills.ModuleOwner the project's go.mod requires.
//
// It shows what it would do, with a diff per file, and writes only with
// --apply. A skill whose header names no source is the project's own and is
// never touched. A stamped skill edited outside its custom block is reported
// and left as it is unless --force; what sits between its custom markers is
// carried forward either way.
//
// The sources are read from the module cache, where the toolchain keeps every
// module it has downloaded. A version not there yet is downloaded with `go mod
// download`, the one step here that may reach the network -- which is why the
// doctor, which never starts the toolchain, speaks about skills only once that
// has happened.
func skillsSync(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("skills:sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	apply := flags.Bool("apply", false, "write the changes instead of only showing them")
	force := flags.Bool("force", false, "replace a skill edited outside its custom block, keeping the block")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("skills:sync: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("skills:sync: %q is not an argument this command takes", flags.Arg(0))
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	origins, err := skills.Origins(root)
	if err != nil {
		return fmt.Errorf("skills:sync: %w", err)
	}
	var received []skills.Received
	for _, origin := range origins {
		dir, err := sourceOf(root, origin)
		if err != nil {
			return fmt.Errorf("skills:sync: %w", err)
		}
		found, err := origin.Read(dir)
		if err != nil {
			return fmt.Errorf("skills:sync: %w", err)
		}
		received = append(received, skills.Received{Origin: origin, Skills: found})
	}

	changes, err := skills.Plan(root, received, *force)
	if err != nil {
		return fmt.Errorf("skills:sync: %w", err)
	}
	printSkillChanges(stdout, received, changes)

	pending := 0
	for _, c := range changes {
		if c.Action == skills.Create || c.Action == skills.Update {
			pending++
		}
	}
	if !*apply {
		if pending > 0 {
			fmt.Fprintf(stdout, "\nnothing was written; aru skills:sync --apply writes the %d file(s) above marked create or update\n", pending)
		}
		return nil
	}

	written, err := skills.Apply(root, changes)
	if err != nil {
		return fmt.Errorf("skills:sync: %w", err)
	}
	fmt.Fprintf(stdout, "\nwrote %d file(s)\n", len(written))
	if conflicts := count(changes, skills.Conflict); conflicts > 0 {
		return fmt.Errorf("skills:sync: %d skill(s) edited outside their custom block were left as they are; --force replaces those edits", conflicts)
	}
	return nil
}

// skillsSyncHelp is the body `aru skills:sync --help` prints under the usage
// line.
//
// It names the five words a preview prints because they are a closed set, and
// says where skills come from because that is the part nobody guesses.
const skillsSyncHelp = `Nothing is written until --apply. Without it the command reads where each skill
comes from and prints what a sync does to it -- create, update, unchanged,
conflict, kept -- and the diff of every file it would write.

A skill comes from the skeleton, at the version this aru creates projects from,
or from a module under github.com/hyz-is/ that go.mod requires, which hands over
the skills it marks audience: app. What is written carries source and digest
under metadata in its frontmatter.

kept is a skill with no source there, which is the project's own, and it is
never touched. conflict is a skill edited outside its custom block: --force
replaces the edit, and what sits between the custom markers is carried forward
either way.`

// sourceOf answers the directory holding an origin's source, downloading the
// version into the module cache when it is not on disk.
//
// A directory replace is read as it is and never downloaded: the build reads
// the directory, and the skills of the version go.mod names beside it would be
// an answer about something the project does not use.
func sourceOf(root string, origin skills.Origin) (string, error) {
	if dir, ok := origin.Dir(); ok {
		return dir, nil
	}
	if origin.Replaced != "" {
		return "", fmt.Errorf("go.mod replaces %s with %s, which is not a directory", origin.Module, origin.Replaced)
	}

	module := origin.Module + "@" + origin.Version
	cmd := buildcache.Command("mod", "download", "-json", module)
	cmd.Dir = root
	out, runErr := cmd.Output()
	var downloaded struct {
		Dir   string
		Error string
	}
	_ = json.Unmarshal(out, &downloaded)
	switch {
	case downloaded.Error != "":
		return "", fmt.Errorf("go mod download %s: %s", module, downloaded.Error)
	case runErr != nil || downloaded.Dir == "":
		return "", fmt.Errorf("go mod download %s did not report where it put the source: %v", module, runErr)
	}
	return downloaded.Dir, nil
}

// printSkillChanges writes where the skills come from, one line per skill with
// what a sync does to it, and then the diff of every skill it would write or
// was stopped from writing.
func printSkillChanges(w io.Writer, received []skills.Received, changes []skills.Change) {
	for _, r := range received {
		fmt.Fprintf(w, "%s: %d skill(s) for an application\n", r.Origin.Label(), len(r.Skills))
	}
	if len(changes) == 0 {
		return
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Action, c.Path, c.Origin)
	}
	_ = tw.Flush()
	for _, c := range changes {
		if c.Reason != "" {
			fmt.Fprintf(w, "\n%s: %s\n", c.Path, c.Reason)
		}
	}
	for _, c := range changes {
		switch c.Action {
		case skills.Create, skills.Update, skills.Conflict:
			if d := skills.Diff(c.Path, c.Existing, c.Content); d != "" {
				fmt.Fprintf(w, "\n%s", d)
			}
		}
	}
}

func count(changes []skills.Change, action skills.Action) int {
	n := 0
	for _, c := range changes {
		if c.Action == action {
			n++
		}
	}
	return n
}
