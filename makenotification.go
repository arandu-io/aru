package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/arandu-io/aru/internal/gen"
)

// makeNotificationUsage is the usage line of make:notification, in the
// dispatch table and in its refusal, naming every flag the command accepts.
const makeNotificationUsage = `aru make:notification <Name> [--channels=mail,database] [--force] [--dry-run]`

// makeNotification writes one notification on hesape/notifications: the type,
// the channels it travels by, the representation each of them needs, and the
// test that asks each representation what its channel asks before it sends.
func makeNotification(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("make:notification", flag.ContinueOnError)
	fs.SetOutput(stderr)
	via := fs.String("channels", "mail", "the channels it travels by, separated by commas: "+strings.Join(gen.NotificationChannels, ", "))
	force := fs.Bool("force", false, "overwrite an existing notification and its test, preserving the custom blocks")
	dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

	name, args := takeName(args)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("make:notification: %w", err)
	}
	if name == "" {
		return fmt.Errorf("usage: %s", makeNotificationUsage)
	}
	if err := checkFlatTree("make:notification", name); err != nil {
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

	// The order of NotificationChannels, whatever order they were typed in: the
	// same flags in another order are the same notification, and the same bytes.
	asked := map[string]bool{}
	for _, c := range strings.Split(*via, ",") {
		if c = strings.TrimSpace(c); c != "" {
			asked[c] = true
		}
	}
	var channels []string
	for _, c := range gen.NotificationChannels {
		if asked[c] {
			channels = append(channels, c)
			delete(asked, c)
		}
	}
	for c := range asked {
		return fmt.Errorf("make:notification: unknown channel %q: a generated notification travels by %s",
			c, strings.Join(gen.NotificationChannels, " or "))
	}

	spec := gen.NotificationSpec{Type: gen.Exported(name), ModulePath: modulePath, Channels: channels}
	files, err := gen.RenderNotification(spec)
	if err != nil {
		return fmt.Errorf("make:notification: %w", err)
	}
	if err := emit("make:notification", root, files, *force, *dryRun, stdout); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}

	fmt.Fprintf(stdout, `
The notification is written, and nothing sends it yet. A service sends it, with
the Grant its policy issued, through the Notifier bootstrap/app.go builds once
with the channels the application has -- by hand, because the wiring is meant
to be readable:

  bootstrap/app.go

      notifier := hnotifications.New([]hnotifications.Channel{
          channels.NewMail(mailer),                                 // a channels.Mailer
          channels.NewDatabase(hnotifications.NewTableStore(db)),
      })

  the service that decides to tell somebody

      err := s.notifier.Send(ctx, g, recipient, notifications.%s{})

importing hnotifications "github.com/arandu-io/hesape/notifications", the
channels package beside it, and notifications "%s".
`, spec.Type, spec.NotificationsImport())
	return nil
}
