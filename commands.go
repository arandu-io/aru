package main

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// command is one CLI entry. The list is a slice, not a map: the order of the
// help output is part of the interface, and a map would shuffle it on every run.
type command struct {
	name  string
	usage string
	desc  string
	run   func(args []string, stdout, stderr io.Writer) error
}

// commands is the whole CLI surface. Phase 1 implements the four commands the
// roadmap requires; the rest state which phase they arrive in, because an empty
// command that pretends to work is worse than one that says it does not.
var commands = []command{
	{
		name:  "key:generate",
		usage: "aru key:generate",
		desc:  "generate the 32-byte APP_KEY",
		run:   keyGenerate,
	},
	{
		name:  "serve",
		usage: "aru serve [-- flags for the application]",
		desc:  "run the application from the current project",
		run:   delegate("serve"),
	},
	{
		name:  "migrate",
		usage: "aru migrate",
		desc:  "apply the migrations collected from the registered modules",
		run:   delegate("migrate"),
	},
	{
		name:  "migrate:rollback",
		usage: "aru migrate:rollback",
		desc:  "undo the last batch of migrations",
		run:   delegate("migrate:rollback"),
	},
	{
		name:  "migrate:status",
		usage: "aru migrate:status",
		desc:  "show which migrations ran, and in which batch",
		run:   delegate("migrate:status"),
	},
	{
		name:  "migrate:fresh",
		usage: "aru migrate:fresh",
		desc:  "roll everything back and migrate again (development only)",
		run:   delegate("migrate:fresh"),
	},
	{
		name:  "routes",
		usage: "aru routes",
		desc:  "list the registered routes, by module",
		run:   delegate("routes"),
	},
	{
		name:  "db:seed",
		usage: "aru db:seed [--class=<name>]",
		desc:  "run the seeders in database/seeders (DatabaseSeeder by default)",
		run:   delegate("db:seed"),
	},
	{
		name:  "new",
		usage: "aru new <name>",
		desc:  "create a new project from the skeleton",
		run:   notYet("new", 2),
	},
	{
		name:  "make:module",
		usage: `aru make:module <name> --fields "..."`,
		desc:  "generate a full module: entity, policy, repository, service, request, routes, tests",
		run:   notYet("make:module", 2),
	},
	{
		name:  "make:policy",
		usage: "aru make:policy <entity>",
		desc:  "generate a policy for an existing entity",
		run:   notYet("make:policy", 2),
	},
	{
		name:  "doctor",
		usage: "aru doctor [--strict]",
		desc:  "check that the project honors the framework contracts",
		run:   notYet("doctor", 2),
	},
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// usage prints the command list. No emoji and no exclamation marks: the tone is
// in docs/04-marca.md, and it is sober everywhere, including here.
func usage(w io.Writer) {
	fmt.Fprint(w, "arandu — a Go framework for SaaS\n\nusage: aru <command> [arguments]\n\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.desc)
	}
	fmt.Fprint(tw, "\n  help\tthis list\n  version\tthe aru version\n")
	_ = tw.Flush()
}

// notYet reports the phase a command arrives in, and points at the document that
// says why. A command that silently does nothing costs more than one that refuses.
func notYet(name string, phase int) func([]string, io.Writer, io.Writer) error {
	return func([]string, io.Writer, io.Writer) error {
		return fmt.Errorf("%s: phase %d, not implemented yet (see 03-roadmap-fases.md)", name, phase)
	}
}
