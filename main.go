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
// by another tool. Same split as Laravel and artisan.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

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
		fmt.Fprintf(stderr, "unknown command: %s\n\n", name)
		usage(stderr)
		return 1
	}

	if err := cmd.run(rest, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}
