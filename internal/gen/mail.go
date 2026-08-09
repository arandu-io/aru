package gen

import (
	"path/filepath"
	"strings"
)

// MailSpec is one mailable to write.
type MailSpec struct {
	// Type is the exported Go type: WelcomeEmail.
	Type string
	// ModulePath is the project's, so the generated imports resolve.
	ModulePath string
	// Subject is the subject line. Empty derives one from the type.
	Subject string
	// Fields are what the message carries into its views.
	Fields []Field
}

// View is the name the two templates are registered under: mail.welcome-email.
func (s MailSpec) View() string { return "mail." + Kebab(s.Type) }

// SubjectLine is what the envelope carries.
func (s MailSpec) SubjectLine() string {
	if s.Subject != "" {
		return s.Subject
	}
	return Humanise(s.Type)
}

// RenderMail writes the mailable and its two views.
func RenderMail(s MailSpec) ([]File, error) {
	mailable, err := render("mail.go", mailTemplate, s)
	if err != nil {
		return nil, err
	}

	html, err := render("mail.kyse.go", mailHTMLTemplate, s)
	if err != nil {
		return nil, err
	}
	text, err := render("mail-text.kyse.go", mailTextTemplate, s)
	if err != nil {
		return nil, err
	}

	name := Kebab(s.Type)
	return []File{
		{Path: filepath.Join("app", "Mail", s.Type+".go"), Content: mailable},
		{Path: filepath.Join("resources", "views", "mail", name+".kyse.go"), Content: html},
		{Path: filepath.Join("resources", "views", "mail", name+"-text.kyse.go"), Content: text},
	}, nil
}

// Kebab turns WelcomeEmail into welcome-email.
func Kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// Humanise turns WelcomeEmail into "Welcome email".
func Humanise(s string) string {
	words := strings.Fields(strings.TrimSpace(Kebab(s)))
	spaced := strings.ReplaceAll(strings.Join(words, " "), "-", " ")
	if spaced == "" {
		return spaced
	}
	return strings.ToUpper(spaced[:1]) + spaced[1:]
}

const mailTemplate = `package mail

import (
	"github.com/arandu-io/framework/mail"
)

// {{.Type}} is one message this application sends.
//
// An Envelope and a Content, and nothing else. There is no ShouldQueue: sending
// on the queue is a job that calls Send, and having it decided by an interface
// the type implements somewhere else is how a call that sometimes blocks for two
// seconds becomes impossible to reason about from the line you are reading.
type {{.Type}} struct {
{{- range .Fields}}
	// {{.GoName}} is the {{.Label}} this message carries.
	{{.GoName}} {{.ViewType}}
{{- end}}
}

// Envelope is who it is from and what it says it is.
//
// From is left empty on purpose: the Mailer fills in the application's address,
// so the one place it is configured is config/mail.go. A message that names its
// own sender is one that keeps sending from the old domain after a migration.
func (m {{.Type}}) Envelope() mail.Envelope {
	return mail.Envelope{
		Subject: "{{.SubjectLine}}",

		// arandu:begin custom
		// Tags and Metadata reach the providers that support them, and are how a
		// dashboard tells password resets apart from invoices.
		// arandu:end custom
	}
}

// Content is what the body is made of.
//
// Two views, because a message with no plain-text part is filed as spam more
// often and shows nothing at all in a client that does not render HTML. Both
// render from this struct, so a field that does not exist is a compile error at
// the line of the .kyse.go rather than a blank space in somebody's inbox.
func (m {{.Type}}) Content() mail.Content {
	return mail.Content{
		View: "{{.View}}",
		Data: m,
	}
}

// Compile-time proof that this is a mailable.
var _ mail.Mailable = {{.Type}}{}
`

const mailHTMLTemplate = `//go:build kyse

package mail

import appmail "<%.ModulePath%>/app/Mail"

@go
// The data this message renders from, named rather than declared: it is
// app/Mail/<%.Type%>.go that owns it, because that is where it is filled in.
type <%.Type%>Data = appmail.<%.Type%>
@endgo

<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title><%.SubjectLine%></title>
</head>
{{-- Inline styles, and that is not laziness.
     A mail client strips <style> and ignores an external stylesheet, so the
     class names the rest of this application uses reach nothing here. This is
     the one place in the project where a hex value belongs in the markup. --}}
<body style="margin:0;padding:24px;background:#f6f6f6;font-family:system-ui,-apple-system,'Segoe UI',sans-serif;color:#111">
	<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;margin:0 auto">
		<tr>
			<td style="background:#fff;border:1px solid #e5e5e5;border-radius:8px;padding:32px">
				<h1 style="margin:0 0 16px;font-size:20px;font-weight:600"><%.SubjectLine%></h1>

				{{-- arandu:begin custom --}}
				<p style="margin:0 0 16px;font-size:15px;line-height:1.6">
					Write the message here. It survives regeneration.
				</p>
				{{-- arandu:end custom --}}
			</td>
		</tr>
		<tr>
			<td style="padding:16px 8px;font-size:12px;color:#777">
				You are receiving this because of an action on your account.
			</td>
		</tr>
	</table>
</body>
</html>
`

const mailTextTemplate = `//go:build kyse

package mail

import appmail "<%.ModulePath%>/app/Mail"

@go
// The plain-text part, rendering from the same data as the HTML one.
type <%.Type%>TextData = appmail.<%.Type%>
@endgo

<%.SubjectLine%>

{{-- arandu:begin custom --}}
Write the message here, in words. It survives regeneration.
{{-- arandu:end custom --}}

--
You are receiving this because of an action on your account.
`
