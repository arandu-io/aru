// Command aru is the Arandu CLI.
//
// It does two kinds of work, and the split matters. Commands that only touch
// files -- key:generate, new, make:*, doctor -- run here. Commands that need the
// registered modules -- serve, migrate, routes -- are delegated to the project's
// own binary, because only that binary knows which modules exist. Nothing is
// resolved by reflection, and everything the CLI generates is readable,
// committable Go.
//
// The binary is called aru rather than arandu: the arandu name is already taken
// by another tool: the framework and the tool that drives it are separate names.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// Version is what this build reports, for the check that refuses a CLI older
// than a project asks for.
func Version() string { return version }

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run holds the whole dispatch so tests can drive the CLI without a subprocess.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 1
	}

	name := args[0]
	rest := args[1:]

	switch name {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "aru %s\n", version)
		return 0
	}

	cmd, ok := lookup(name)
	if !ok {
		// A family name with no command behind it -- `aru font` rather than `aru
		// font:add`. Answered here, before anything is delegated: inside a
		// project the name would otherwise go to the application's binary, which
		// replies with the commands it knows and none of them is the one being
		// reached for. Outside one it would print the whole table, with the ones
		// wanted somewhere in the middle of it.
		if of := subcommands(name); len(of) > 0 {
			fmt.Fprintf(stderr, "%s is a family of commands, not a command:\n\n", name)
			listFamily(stderr, of)
			return 1
		}

		// Not one of ours, so it may be one of the application's.
		//
		// routes/console.go is where a project declares its own commands, and
		// the project binary dispatches them through routes.Lookup. A console
		// that ships inside the project can dispatch its own commands directly;
		// here aru is a separate binary, and without this the command a person just generated
		// with `aru make:command` answers "unknown command" from the tool that
		// generated it.
		//
		// The good error survives: outside a project this falls through to the
		// usage below, and inside one the project binary lists what it knows.
		if _, err := projectRoot(); err == nil {
			if err := delegate(name)(rest, stdout, stderr); err != nil {
				fmt.Fprintf(stderr, "%s\n", err)
				return 1
			}
			return 0
		}
		fmt.Fprintf(stderr, "unknown command: %s\n\n", name)
		usage(stderr)
		return 1
	}

	// --help is answered before the command runs, so that every one of them
	// answers it and none has to remember to. A command reached this way has not
	// looked at its arguments, found a project or touched a file.
	if wantsHelp(rest) {
		explain(cmd, stdout)
		return 0
	}

	if err := cmd.run(rest, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}
