package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeControllerUsage is the usage line of make:controller, in the dispatch
// table and in its refusal, naming every flag the command accepts.
const makeControllerUsage = `aru make:controller <Name> [--resource | --singleton | --invokable] ` +
	`[--parent=<resource>] [--action=<name>] [--force]`

// makeController writes one controller.
//
// It exists because somebody porting an application does not port a module:
// they port a controller, then the next one. `aru make:module` writes twelve
// files from an entity; this writes one file from a name.
func makeController(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:controller", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resource := fs.Bool("resource", false, "emit the seven actions Router.Resource registers")
	singleton := fs.Bool("singleton", false, "emit show, edit and update with no id, for Router.Singleton")
	invokable := fs.Bool("invokable", false, "emit one action, Invoke, for Router.Invokable")
	parent := fs.String("parent", "", "nest the resource or singleton under this one, as the route table names it: projects")
	action := fs.String("action", "", "add one named action on a record of the resource: publish")
	force := fs.Bool("force", false, "overwrite an existing controller, preserving the custom block")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:controller: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeControllerUsage)
	}
	if err := checkFlatTree("make:controller", name); err != nil {
		return err
	}

	kind := gen.KindPlain
	picked := 0
	for _, k := range []struct {
		on   bool
		kind gen.Kind
	}{{*resource, gen.KindResource}, {*singleton, gen.KindSingleton}, {*invokable, gen.KindInvokable}} {
		if k.on {
			kind = k.kind
			picked++
		}
	}
	if picked > 1 {
		return fmt.Errorf("make:controller: --resource, --singleton and --invokable ask for different controllers; pick one")
	}
	// A parent or an action is a question about a resource, so asking one
	// without naming the shape asks for a resource rather than for an error.
	if kind == gen.KindPlain && (*parent != "" || *action != "") {
		kind = gen.KindResource
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

	// A singleton is one thing where it is reached -- settings, billing -- so
	// its segment is its name, not a plural of it.
	segment := entity.Resource()
	if kind == gen.KindSingleton {
		segment = strings.ReplaceAll(entity.Name, "_", "-")
	}

	stub := gen.Stub{
		Type:       suffixed(name, "Controller"),
		ModulePath: modulePath,
		Resource:   segment,
		Entity:     entity.Entity(),
		Kind:       kind,
		Parent:     strings.ToLower(strings.ReplaceAll(*parent, "_", "-")),
		Action:     strings.ToLower(strings.ReplaceAll(*action, "_", "-")),
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

// routeLines are the lines that register a controller, one per line, written
// for the custom block of routes/web.go.
//
// The method of a named action and of an invokable is the string the route
// table already uses for its own lines -- "POST" -- and not net/http's
// constant: routes/web.go imports the framework's http package under that
// name, and it declares no MethodPost.
func routeLines(s gen.Stub, m gen.Module) []string {
	switch s.Kind {
	case gen.KindResource:
		lines := []string{fmt.Sprintf("r.Resource(%q, d.%s)", s.RouteResource(), m.Entity())}
		if s.Action != "" {
			lines = append(lines, fmt.Sprintf("r.ResourceAction(%q, %q, %q, d.%s.%s)",
				"POST", s.RouteResource(), s.Action, m.Entity(), s.ActionMethod()))
		}
		return lines
	case gen.KindSingleton:
		return []string{fmt.Sprintf("r.Singleton(%q, d.%s)", s.RouteResource(), m.Entity())}
	case gen.KindInvokable:
		return []string{fmt.Sprintf("r.Invokable(%q, %q, d.%s).Name(%q)", "POST", "/"+s.Resource, m.Entity(), s.Resource)}
	}
	return nil
}

// wiringController is what to do with the file that was just written.
//
// It is a function so it can be tested, for the same reason `wiring` is: an
// instruction that does not compile is worse than no instruction, because it is
// followed.
func wiringController(s gen.Stub, m gen.Module) string {
	route := "      (no route yet -- register the actions you write here)"
	if lines := routeLines(s, m); len(lines) > 0 {
		route = "      " + strings.Join(lines, "\n      ")
	}

	construct := fmt.Sprintf("controllers.New%s()", s.Type)
	if s.Service != "" {
		construct = fmt.Sprintf("controllers.New%s(services.New%s(db))", s.Type, s.Service)
	}

	tail := ""
	switch s.Kind {
	case gen.KindResource:
		tail = "\nThe actions answer 501 until you write them, and Resource registers only\n" +
			"the ones this controller implements: delete the interface line and the method\n" +
			"together for the ones it does not.\n"
		if s.Parent != "" {
			tail += fmt.Sprintf("\nThe listing, the form and the store answer under %s, and read the parent\n"+
				"from ctx.Param(%q); the other four answer at %s and read the record from\n"+
				"ctx.Param(%q). The parent is where the person navigated, never whose data it\n"+
				"is: the service loads it under the Grant and filters by it.\n",
				s.CollectionPath(), s.ParentParam(), s.MemberPath(), s.MemberParam())
		}
		if s.Action != "" {
			tail += fmt.Sprintf("\n%s answers POST %s/%s, named %s.%s. Change the method to\n"+
				"PUT, PATCH or DELETE if it fits better -- never GET, because it changes state.\n",
				s.ActionMethod(), s.MemberPath(), s.Action, s.RouteResource(), s.Action)
		}
	case gen.KindInvokable:
		tail = "\nThe route is POST because an action that stands alone usually changes\n" +
			"something; make it GET only if it changes nothing.\n"
	}

	return fmt.Sprintf(`
The file is written, the wiring is not. Two places, by hand, because the wiring
is meant to be readable:

  routes/web.go -- the field on Deps, and the routes inside the custom block

      %s *controllers.%s

%s

  bootstrap/app.go -- in the routes.Deps literal

      %s: %s,
%s
There is no view yet. `+"`aru make:module`"+` writes the four screens because it knows
the fields; this command does not, so ctx.View comes with the screen you write.
`, m.Entity(), s.Type, route, m.Entity(), construct, tail)
}
