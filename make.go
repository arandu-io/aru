package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// The granular make:* commands share this file.
//
// They are what somebody porting an application types on the first day: one
// class at a time, in a tree they already know. `aru make:module` writes an
// entity and everything around it; these write one file.
//
// Everything about how they behave is here, once. A second command that read its
// name differently, or refused an existing file with another message, would be a
// second way to do one thing inside the generator itself.

// takeName pulls the positional name off the front of the arguments.
//
// The name comes first and the flags after it -- which is how everyone types it,
// and what the flag package does not support: it stops parsing at the first
// positional argument.
func takeName(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// checkFlatTree refuses a name that asks for a nested package.
//
// A nested name like Admin/UserController would mean a nested package. Here that
// would mean a second package, a second import alias and a second shape of
// wiring, for a tree that is deliberately flat: every controller is in
// app/Http/Controllers, which is why the name carries the entity.
func checkFlatTree(command, name string) error {
	if !strings.ContainsAny(name, `/\`) {
		return nil
	}
	return fmt.Errorf("%s: %q names a nested package, and the tree is flat: "+
		"every file of a kind shares one directory and one package, which is why the name carries the entity. "+
		"Write it as one identifier", command, name)
}

// suffixed adds a suffix idempotently: Invoice and InvoiceController both give
// InvoiceController.
func suffixed(name, suffix string) string {
	base := strings.TrimSuffix(name, suffix)
	if base == "" {
		base = name
	}
	return gen.Exported(base) + suffix
}

// unsuffixed removes a suffix idempotently, for the commands that name the
// entity rather than the type: InvoiceSeeder and Invoice both give Invoice.
func unsuffixed(name, suffix string) string {
	base := strings.TrimSuffix(gen.Exported(name), suffix)
	if base == "" {
		base = gen.Exported(name)
	}
	return base
}

// emit writes the generated files and reports what happened.
//
// The message for a file that already exists says what --force actually does,
// with all the letters: running `aru make:controller Invoice --force` after
// `aru make:module invoice` replaces seven implemented actions with seven 501s,
// and the only thing that survives is the custom block. A generic "rerun with
// --force" would be an instruction that eats work.
func emit(command, root string, files []gen.File, force, dryRun bool, stdout io.Writer) error {
	if dryRun {
		for _, f := range files {
			fmt.Fprintf(stdout, "%s (%d bytes)\n", f.Path, len(f.Content))
		}
		return nil
	}

	written, skipped, err := gen.Write(root, files, force)
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		return fmt.Errorf("%s: %s already exists. Rerun with --force to regenerate it -- "+
			"the custom block is preserved and everything else, including any code you wrote outside it, is not",
			command, strings.Join(skipped, ", "))
	}
	for _, p := range written {
		fmt.Fprintln(stdout, "created", p)
	}
	return nil
}
