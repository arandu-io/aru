package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/arandu-io/aru/internal/gen"
)

// makeMail writes one mailable and the two views it renders.
//
// A mailable is an Envelope and a Content and nothing else, which is Laravel's
// shape. What is different is what it does not have: no ShouldQueue. Sending on
// the queue here is a job that calls Send, and the difference is visible at the
// call site rather than in an interface the type opts into at a distance.
//
// Two views, not one. A message with no plain-text part is filed as spam more
// often and shows nothing at all in a client that does not render HTML, and the
// only reliable way to get one is for the generator to write it.
func makeMail(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:mail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	subject := fs.String("subject", "", "the subject line (default: derived from the type)")
	fields := fs.String("fields", "", `what the message carries, as "name:type" separated by commas`)
	force := fs.Bool("force", false, "overwrite an existing mailable, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:mail: %w", err)
	}
	if name == "" {
		return fmt.Errorf(`usage: aru make:mail <Name> [--subject "Welcome"] [--fields "name:string,link:string"] [--force]`)
	}
	if err := checkFlatTree("make:mail", name); err != nil {
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

	spec := gen.MailSpec{Type: gen.Exported(name), ModulePath: modulePath, Subject: *subject}
	if *fields != "" {
		parsed, err := gen.ParseFields(*fields)
		if err != nil {
			return fmt.Errorf("make:mail: %w", err)
		}
		spec.Fields = parsed
	}

	files, err := gen.RenderMail(spec)
	if err != nil {
		return fmt.Errorf("make:mail: %w", err)
	}
	if err := emit("make:mail", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
One line in bootstrap/app.go, if this is the first mailable -- the views
register themselves from init(), and a package nobody imports registers
nothing:

    _ "%s/storage/framework/views/mail"

Then `+"`aru view:build`"+`, and send it:

    mailer.To(user.Email).Send(ctx, mail.%s{})

The mailer is wired in bootstrap/app.go. In development it is the log
transport, which prints the message instead of sending it -- an application
that needs a mail server to run is one nobody runs.
`, modulePath, spec.Type)
	return nil
}
