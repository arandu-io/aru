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
		name:  "schedule:list",
		usage: "aru schedule:list",
		desc:  "list the scheduled tasks, with the next run of each",
		run:   delegate("schedule:list"),
	},
	{
		name:  "schedule:run",
		usage: "aru schedule:run <id> [--tenant=<id>]",
		desc:  "run one scheduled task now, on the same path the scheduler uses",
		run:   delegate("schedule:run"),
	},
	{
		name:  "work",
		usage: "aru work [--queue=default] [--workers=4]",
		desc:  "drain a job queue; the same image with another argument",
		run:   delegate("work"),
	},
	{
		name:  "build",
		usage: "aru build [--docker] [--version=v1.2.3] [--output=bin/app]",
		desc:  "compile one static binary, with its checksum, or a container image",
		run:   build,
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
		name:  "dev",
		usage: "aru dev [-- flags for the application]",
		desc:  "build the views, run the application, and restart it on every change",
		run:   dev,
	},
	{
		name:  "view:build",
		usage: "aru view:build [--watch]",
		desc:  "compile the templates and the stylesheet, with no Node involved",
		run:   viewBuild,
	},
	{
		name:  "new",
		usage: "aru new <name> [--module github.com/you/name]",
		desc:  "create a new project from the skeleton",
		run:   newProject,
	},
	{
		name:  "make:module",
		usage: `aru make:module <name> --fields "title:string!,amount:money" [--tenant] [--force]`,
		desc:  "generate a full module: entity, policy, repository, service, request, routes, tests",
		run:   makeModule,
	},
	{
		name:  "make:auth",
		usage: "aru make:auth [--force]",
		desc:  "publish the sign-in screens into the project, yours to edit",
		run:   makeAuth,
	},
	{
		name:  "generate",
		usage: "aru generate <spec.yaml> [--check] [--dry-run] [--force]",
		desc:  "generate a module from a specification; the model writes the spec, never the Go",
		run:   generate,
	},
	{
		name:  "schema",
		usage: "aru schema [--output module.schema.json]",
		desc:  "print the JSON Schema a specification is written against",
		run:   schemaCommand,
	},
	{
		name:  "make:policy",
		usage: "aru make:policy <entity>",
		desc:  "generate a policy for an existing entity",
		run:   makePolicy,
	},
	{
		name:  "trace",
		usage: "aru trace [<request_id>] [--host http://127.0.0.1:8080]",
		desc:  "reconstruct a request in the terminal, from the running application",
		run:   trace,
	},
	{
		name:  "doctor",
		usage: "aru doctor [--strict]",
		desc:  "check that the project honors the framework contracts",
		run:   runDoctor,
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
