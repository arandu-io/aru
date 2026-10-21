package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeJobUsage is the usage line of make:job, in the dispatch table and in
// its refusal, naming every flag the command accepts.
const makeJobUsage = `aru make:job <Name> [--event-name=invoice.send] [--fields "invoice_id:uuid"] [--services=Invoice,Customer] [--force] [--dry-run]`

// makeJob writes one background job.
//
// It writes a background job. There is no --sync: one queue, and a synchronous
// job is a function call. There is no --queue either -- the queue is
// a constant in the generated file, and whoever chooses one at push time already
// has hjobs.New(g, queue, ...).
func makeJob(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:job", flag.ContinueOnError)
	fs.SetOutput(stderr)
	eventName := fs.String("event-name", "", "the routing key stored in Job.Name (default: derived from the type)")
	fields := fs.String("fields", "", `the payload, as "name:type" separated by commas`)
	uses := fs.String("services", "", "the entities whose services the handler takes, separated by commas: Invoice,Customer")
	force := fs.Bool("force", false, "overwrite an existing job, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:job: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeJobUsage)
	}
	if err := checkFlatTree("make:job", name); err != nil {
		return err
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	modulePath, err := readModulePath(root)
	if err != nil {
		return err
	}

	spec := gen.JobSpec{Type: gen.Exported(name), ModulePath: modulePath}
	spec.EventName = *eventName
	if spec.EventName == "" {
		spec.EventName = gen.DefaultEventName(spec.Type)
	}
	if *fields != "" {
		parsed, err := gen.ParseFields(*fields)
		if err != nil {
			return fmt.Errorf("make:job: %w", err)
		}
		spec.Fields = parsed
	}

	for _, entity := range strings.Split(*uses, ",") {
		if entity = strings.TrimSpace(entity); entity == "" {
			continue
		}
		entity = gen.Exported(unsuffixed(entity, "Service"))
		service := gen.Module{Name: gen.Normalize(entity)}.ServiceType()
		if _, err := os.Stat(filepath.Join(root, "app", "Services", service+".go")); err != nil {
			return fmt.Errorf("make:job: app/Services/%s.go does not exist -- a handler takes a service the "+
				"application already builds; `aru make:module %s` writes it", service, gen.Normalize(entity))
		}
		spec.Services = append(spec.Services, entity)
	}

	file, err := gen.RenderJob(spec)
	if err != nil {
		return fmt.Errorf("make:job: %w", err)
	}
	if err := emit("make:job", root, []gen.File{file}, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprint(stdout, wiringJob(spec))
	return nil
}

// wiringJob is what to do with the handler that was just written: register it
// where the built services are, and say how it receives them.
//
// The handler takes its services through its constructor, and the provider's
// RegisterJobs is where they are: bootstrap/app.go hands it to WithJobs, and
// the provider holds every service it built at boot. A handler that built its
// own would be a second application, subtly different from the one a request
// reaches.
func wiringJob(s gen.JobSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
The handler is written, the registration is not.

  app/Providers/AppServiceProvider.go -- in RegisterJobs, the function
  bootstrap/app.go hands to WithJobs

      w.Handle(appjobs.%s, %s)

  and the import, aliased because jobs is also the queue's own package name

      appjobs %q
`, s.Const(), s.Constructor(), s.ModulePath+"/app/Jobs")

	if deps := s.Deps(); len(deps) > 0 {
		fmt.Fprintf(&b, `
The handler takes its services as the parameters of New%s,
and RegisterJobs passes them from the provider, which holds the services it
built at boot. The provider carries each one as a field -- add the ones it does
not carry yet, and fill them where NewAppServiceProvider builds them:

`, s.Handler())
		for _, d := range deps {
			fmt.Fprintf(&b, "      %s *services.%s\n", d.AppField, d.Type)
		}
		b.WriteString("\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "      p.%s = %s\n", d.AppField, serviceConstruction(gen.Module{Name: gen.Normalize(unsuffixed(d.Type, "Service"))}))
		}
	} else {
		fmt.Fprintf(&b, `
New%s takes nothing yet. A service the job calls is a
parameter of it and a field of the handler -- regenerate with
--services=<Entity>,... or add both by hand -- and RegisterJobs passes it from
the provider, which holds the services it built at boot. The handler never
builds its own.
`, s.Handler())
	}
	b.WriteString(`
Then:

    aru queue:work
`)
	return b.String()
}
