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
		// queue:work, the conventional name. What the person types is the
		// half that needs parity -- the string handed to the project binary is
		// an internal protocol and promises nothing to anyone, so it stays as it
		// is and no project generated before today stops working.
		name:  "queue:work",
		usage: "aru queue:work [--queue=default] [--workers=4]",
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
		// route:list, the conventional name, for the same reason as
		// queue:work above.
		name:  "route:list",
		usage: "aru route:list",
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
		usage: "aru view:build",
		desc:  "compile the templates and the stylesheet, with no Node involved",
		run:   viewBuild,
	},
	{
		name: "font:add",
		usage: `aru font:add "Young Serif" --as display [--weight 400..700] [--subset latin,latin-ext]` + "\n" +
			`             aru font:add --file ./Arandu.woff2 --family "Arandu" --as display [--metrics-from ./Arandu.ttf]`,
		desc: "vendor a font into the project, from the catalogue or from a file of your own",
		run:  fontAdd,
	},
	{
		name:  "font:search",
		usage: "aru font:search [query] [--category serif] [--variable] [--limit 25|--all]",
		desc:  "browse the catalogue: what exists, its weights and its licence",
		run:   fontSearch,
	},
	{
		name:  "font:info",
		usage: `aru font:info "Young Serif"`,
		desc:  "one family in full: weights, axes, scripts, who drew it",
		run:   fontInfo,
	},
	{
		name:  "font:list",
		usage: "aru font:list",
		desc:  "show the vendored faces, their licences and what they weigh",
		run:   fontList,
	},
	{
		name:  "font:remove",
		usage: "aru font:remove <display|body>",
		desc:  "drop a vendored face and fall back to the system stack",
		run:   fontRemove,
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
	// The granular family, in the order a migration uses them: the model first,
	// because in Go the struct is the schema, then the request, the controller
	// and what surrounds them. They are read together in `aru help`, and they are
	// what somebody porting one class at a time types on the first day.
	{
		name:  "make:model",
		usage: `aru make:model <Name> --fields "reference:string!u,total:money" [--tenant] [--migration] [--factory] [--force]`,
		desc:  "generate the entity, and the migration and factory the flags ask for",
		run:   makeModel,
	},
	{
		name:  "make:migration",
		usage: `aru make:migration <name> [--create=<table> | --table=<table>] --fields "status:string"`,
		desc:  "generate one migration: a table, or columns added to one",
		run:   makeMigration,
	},
	{
		name:  "make:controller",
		usage: "aru make:controller <Name> [--resource] [--invokable] [--force]",
		desc:  "generate one controller, in the flat app/Http/Controllers package",
		run:   makeController,
	},
	{
		name:  "make:middleware",
		usage: "aru make:middleware <Name> [--force]",
		desc:  "generate one middleware, as the net/http signature every Go server takes",
		run:   makeMiddleware,
	},
	{
		name:  "make:request",
		usage: `aru make:request <Name> [--fields "reference:string!"] [--force]`,
		desc:  "generate one input contract; authorization stays in the Policy",
		run:   makeRequest,
	},
	{
		name:  "make:factory",
		usage: "aru make:factory <Name> [--force]",
		desc:  "generate the factory of an entity, with the fields read off its model",
		run:   makeFactory,
	},
	{
		name:  "make:seeder",
		usage: "aru make:seeder <Name> [--force]",
		desc:  "generate one seeder, for the registry in database/seeders",
		run:   makeSeeder,
	},
	{
		name:  "make:job",
		usage: `aru make:job <Name> [--event-name=invoice.send] [--fields "invoice_id:uuid"] [--force]`,
		desc:  "generate a background job: the payload it carries and the handler that runs it",
		run:   makeJob,
	},
	{
		name:  `make:mail`,
		usage: `aru make:mail <Name> [--subject "Welcome"] [--fields "name:string"] [--force]`,
		desc:  "generate a mailable and the two views it renders, HTML and plain text",
		run:   makeMail,
	},
	{
		name:  "make:command",
		usage: `aru make:command <Name> [--signature=invoice:close] [--description="..."]`,
		desc:  "generate a console command, in app/Console/Commands",
		run:   makeCommand,
	},
	{
		name:  "make:listener",
		usage: `aru make:listener <Name> [--event=invoice.paid]`,
		desc:  "generate an event listener, in app/Listeners",
		run:   makeListener,
	},
	{
		name:  "make:event",
		usage: `aru make:event <Name> --aggregate=invoice [--event-name=invoice.paid] [--fields "..."]`,
		desc:  "generate a domain event, stored in the outbox by the write that caused it",
		run:   makeEvent,
	},
	{
		name:  "make:enum",
		usage: "aru make:enum <Name> --values draft,sent,paid [--int] [--force]",
		desc:  "generate a closed set of values, with the Scan/Value pair the column needs",
		run:   makeEnum,
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
// in 00-meta/DOC-brand.md, and it is sober everywhere, including here.
func usage(w io.Writer) {
	fmt.Fprint(w, "arandu — a Go framework for SaaS\n\nusage: aru <command> [arguments]\n\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.desc)
	}
	fmt.Fprint(tw, "\n  help\tthis list\n  version\tthe aru version\n")
	_ = tw.Flush()
}
