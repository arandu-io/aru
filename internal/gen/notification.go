package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NotificationChannels are the channels make:notification writes a
// representation for. The set is closed: each one is an interface of
// hesape/notifications/channels the type has to satisfy, and a channel name
// nobody can build a message for is a notification nobody receives.
var NotificationChannels = []string{"mail", "database"}

// NotificationSpec is one notification to write.
type NotificationSpec struct {
	// Type is the exported Go type: InvoicePaid.
	Type string
	// ModulePath is the project's, so the generated test imports the type.
	ModulePath string
	// Channels are the ones Via answers, in the order NotificationChannels
	// lists them.
	Channels []string
}

// Key is the stable name the notification is stored and suppressed under:
// invoice-paid.
func (s NotificationSpec) Key() string { return Kebab(s.Type) }

// Subject is the subject line the generated mail starts from.
func (s NotificationSpec) Subject() string { return Humanise(s.Type) }

// Mail and Database report whether the notification travels that way.
func (s NotificationSpec) Mail() bool     { return s.has("mail") }
func (s NotificationSpec) Database() bool { return s.has("database") }

func (s NotificationSpec) has(channel string) bool {
	for _, c := range s.Channels {
		if c == channel {
			return true
		}
	}
	return false
}

// ChannelConstants are the hesape constants Via returns, in order.
func (s NotificationSpec) ChannelConstants() []string {
	out := make([]string, 0, len(s.Channels))
	for _, c := range s.Channels {
		out = append(out, "hnotifications.Channel"+exported(c))
	}
	return out
}

// NotificationsImport is where the generated type lives.
func (s NotificationSpec) NotificationsImport() string { return s.ModulePath + "/app/Notifications" }

// Validate reports what is wrong before a file is written.
func (s NotificationSpec) Validate() error {
	if !IsExportedIdentifier(s.Type) {
		return fmt.Errorf("%q is not a Go type name", s.Type)
	}
	if s.ModulePath == "" {
		return errModulePath
	}
	if len(s.Channels) == 0 {
		return fmt.Errorf("a notification travels by at least one channel: %s", strings.Join(NotificationChannels, ", "))
	}
	seen := map[string]bool{}
	for _, c := range s.Channels {
		known := false
		for _, k := range NotificationChannels {
			known = known || c == k
		}
		if !known {
			return fmt.Errorf("unknown channel %q: a generated notification travels by %s", c, strings.Join(NotificationChannels, " or "))
		}
		if seen[c] {
			return fmt.Errorf("channel %q named twice", c)
		}
		seen[c] = true
	}
	return nil
}

// RenderNotification produces app/Notifications/<Type>.go and its test.
func RenderNotification(s NotificationSpec) ([]File, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	body, err := render(s.Type+".go", notificationTemplate, s)
	if err != nil {
		return nil, err
	}
	test, err := render(s.Type+"_test.go", notificationTestTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{
		{Path: filepath.Join("app", "Notifications", s.Type+".go"), Content: body},
		{Path: filepath.Join("tests", "Unit", s.Type+"Notification_test.go"), Content: test},
	}, nil
}

const notificationTemplate = `package notifications

import (
	hnotifications "github.com/arandu-io/hesape/notifications"
{{- if or .Mail .Database}}
	"github.com/arandu-io/hesape/notifications/channels"
	"github.com/arandu-io/hesape/notifications/messages"
{{- end}}
)

// {{.Type}} is one thing this application tells somebody.
//
// It is sent by a service, after the policy issued the Grant, through the
// Notifier bootstrap/app.go builds with the channels the application has:
//
//	err := s.notifier.Send(ctx, g, recipient, notifications.{{.Type}}{})
//
// What it carries is its fields, filled by the service that sends it. It never
// reads a model or the database itself: by the time it is built, everything it
// says is known.
type {{.Type}} struct {
	hnotifications.NotificationBase

	// arandu:begin custom
	// What the message needs to say, as fields: Number string, Amount int64.
	// arandu:end custom
}

// {{.Type}}Key is the stable name of this kind of notification. It is written
// to the stored row and is what Suppress silences, so it stays the same when
// the type is renamed.
const {{.Type}}Key hnotifications.Key = {{quote .Key}}

// Key names the kind.
func (n {{.Type}}) Key() hnotifications.Key { return {{.Type}}Key }

// Via is which channels this notification takes for this recipient. The
// recipient is an argument because the answer can depend on them: somebody who
// turned e-mail off gets the stored row and nothing else, and that decision
// belongs here rather than in a filter downstream.
func (n {{.Type}}) Via(to hnotifications.Notifiable) []hnotifications.ChannelName {
	return []hnotifications.ChannelName{ {{- join .ChannelConstants ", " -}} }
}
{{- if .Mail}}

// ToMail is the message by e-mail, built for this recipient.
//
// It is written in lines rather than with a template: messages.Mail renders its
// subject, its lines and its button itself, in HTML and in plain text, escaped
// on the way out, so there is no view to keep in step with this method. A link
// in Action has to be absolute -- a mail client has no host to resolve a path
// against -- and the message refuses to send without a subject or a body.
func (n {{.Type}}) ToMail(to hnotifications.Notifiable) messages.Mail {
	message := messages.NewMail().Subject({{quote .Subject}})
	// arandu:begin custom
	message = message.Line("Write the message here, one line at a time.")
	// arandu:end custom
	return message
}
{{- end}}
{{- if .Database}}

// ToDatabase is the row the bell menu renders from: the payload stored in the
// notifications table, as JSON, for the recipient to read back.
func (n {{.Type}}) ToDatabase(to hnotifications.Notifiable) messages.Database {
	return messages.NewDatabase(map[string]any{
		"title": {{quote .Subject}},
		// arandu:begin custom
		// arandu:end custom
	})
}
{{- end}}

// Compile-time proof that the notification answers every channel Via names:
// a channel without its method is a recipient who never receives it.
var (
	_ hnotifications.Notification = {{.Type}}{}
{{- if .Mail}}
	_ channels.MailNotification = {{.Type}}{}
{{- end}}
{{- if .Database}}
	_ channels.DatabaseNotification = {{.Type}}{}
{{- end}}
)
`

const notificationTestTemplate = `package unit_test

import (
	"testing"

	hnotifications "github.com/arandu-io/hesape/notifications"

	notifications "{{.NotificationsImport}}"
)

// TestThe{{.Type}}NotificationCanBeDelivered builds the notification for a
// recipient and asks each channel's representation the question the channel
// asks before it sends: a key that can be stored, the channels Via promised,
// {{- if .Mail}} a mail with a subject and a body,{{end}}
{{- if .Database}} a payload that encodes,{{end}} so a message that would be refused at
// midnight is refused here.
func TestThe{{.Type}}NotificationCanBeDelivered(t *testing.T) {
	n := notifications.{{.Type}}{}
	to := hnotifications.Route(hnotifications.ChannelMail, "ada@example.com")

	if !n.Key().Valid() {
		t.Errorf("key %q cannot be stored: lowercase, dotted, no spaces", n.Key())
	}
	via := n.Via(to)
	want := []hnotifications.ChannelName{ {{- join .ChannelConstants ", " -}} }
	if len(via) != len(want) {
		t.Fatalf("Via = %v, want %v", via, want)
	}
	for i := range want {
		if via[i] != want[i] {
			t.Errorf("Via = %v, want %v", via, want)
		}
	}
{{- if .Mail}}
	if err := n.ToMail(to).Validate(); err != nil {
		t.Errorf("the mail would be refused: %v", err)
	}
{{- end}}
{{- if .Database}}
	if _, err := n.ToDatabase(to).JSON(); err != nil {
		t.Errorf("the stored payload does not encode: %v", err)
	}
{{- end}}
}
// arandu:begin custom
// arandu:end custom
`
