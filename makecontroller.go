package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeController writes one controller.
//
// It is `php artisan make:controller`, and it exists because the developer this
// framework is for does not port a module: they port a controller, then the next
// one. `aru make:module` writes twelve files from an entity; this writes one file
// from a name.
func makeController(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:controller", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resource := fs.Bool("resource", false, "emit the seven actions httpx.Resource registers")
	invokable := fs.Bool("invokable", false, "emit one action, Handle: artisan's --invokable")
	force := fs.Bool("force", false, "overwrite an existing controller, preserving the custom block")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:controller: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: aru make:controller <Name> [--resource] [--invokable] [--force]")
	}
	if err := checkFlatTree("make:controller", name); err != nil {
		return err
	}
	if *resource && *invokable {
		return fmt.Errorf("make:controller: --resource and --invokable ask for two different controllers; pick one")
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	// The resource segment and the field name in Deps come from the entity, the
	// same way `aru make:policy` derives them: one function answers "what is this
	// thing called" for every command.
	base := unsuffixed(name, "Controller")
	if base == "" {
		return fmt.Errorf("make:controller: %q names no entity", name)
	}
	entity := gen.Module{Name: gen.Normalize(base), ModulePath: modulePath}

	kind := gen.KindPlain
	switch {
	case *resource:
		kind = gen.KindResource
	case *invokable:
		kind = gen.KindInvokable
	}

	stub := gen.Stub{
		Type:       suffixed(name, "Controller"),
		ModulePath: modulePath,
		Resource:   entity.Resource(),
		Entity:     entity.Entity(),
		Kind:       kind,
	}

	files, err := gen.GenerateController(stub)
	if err != nil {
		return fmt.Errorf("make:controller: %w", err)
	}
	if err := emit("make:controller", root, files, *force, false, stdout); err != nil {
		return err
	}

	fmt.Fprint(stdout, wiringController(stub, entity))
	return nil
}

// wiringController is what to do with the file that was just written.
//
// It is a function so it can be tested, for the same reason `wiring` is: an
// instruction that does not compile is worse than no instruction, because it is
// followed.
func wiringController(s gen.Stub, m gen.Module) string {
	route := "      (no route yet -- register the actions you write here)"
	switch s.Kind {
	case gen.KindResource:
		route = fmt.Sprintf("      r.Resource(%q, d.%s)", s.Resource, m.Entity())
	case gen.KindInvokable:
		route = fmt.Sprintf("      r.Action(\"GET\", %q, d.%s.Handle).Name(%q)",
			"/"+s.Resource, m.Entity(), s.Resource)
	}

	tail := ""
	if s.Kind == gen.KindResource {
		tail = "\nThe seven actions answer 501 until you write them, and Resource registers only\n" +
			"the ones this controller implements: delete the interface line and the method\n" +
			"together for the ones it does not.\n"
	}

	return fmt.Sprintf(`
The file is written, the wiring is not. Two places, by hand, because the wiring
is meant to be readable:

  routes/web.go -- the field on Deps, and the routes inside the custom block

      %s *controllers.%s

%s

  bootstrap/app.go -- in the routes.Deps literal

      %s: controllers.New%s(sessions, csrf),
%s
There is no view yet. `+"`aru make:module`"+` writes the four screens because it knows
the fields; this command does not, so ctx.View comes with the screen you write.
`, m.Entity(), s.Type, route, m.Entity(), s.Type, tail)
}
