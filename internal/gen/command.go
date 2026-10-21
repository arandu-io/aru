package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Command is one console command to write.
type Command struct {
	// Name is the name of the function that builds it: CloseInvoices.
	Name string
	// Signature is what the person types: "invoice:close".
	Signature string
	// Description is the one line `aru` prints next to it.
	Description string
	// ModulePath is the project's module path, for the import.
	ModulePath string
}

// SignatureOrDefault is what the person types, derived from the name when the
// flag was left out. It is a method so the CLI can print the same value the
// generated file carries, instead of computing it a second time.
func (c Command) SignatureOrDefault() string {
	if c.Signature != "" {
		return c.Signature
	}
	return defaultSignature(c.Name)
}

// DescriptionOrDefault is the one line the console listing prints.
func (c Command) DescriptionOrDefault() string {
	if c.Description != "" {
		return c.Description
	}
	return "Describe what this command does"
}

// Type is the function the command is built by.
func (c Command) Type() string { return exported(c.Name) }

// GenerateCommand writes app/Console/Commands/<Name>.go: a function that
// returns the console.Command.
//
// The difference from the usual shape is where the command becomes reachable
// from. Discovery scans a directory and instantiates what it finds; here the
// command is listed in Commands in routes/console.go, which bootstrap/app.go
// hands to WithCommands, because nothing in this framework finds a type by
// reflection.
//
// The cost is one line to add by hand, and the command prints it. What it buys
// is that the console listing and the compiler read the same list: a command
// that is not in it does not exist, and a command in it with a broken
// signature does not build.
func GenerateCommand(c Command) ([]File, error) {
	if c.ModulePath == "" {
		return nil, errModulePath
	}
	if strings.TrimSpace(c.Name) == "" {
		return nil, fmt.Errorf("make:command: the command needs a name")
	}
	// Through the same two methods the CLI prints from, so the file and the
	// instruction never disagree about what the command is called.
	c.Signature = c.SignatureOrDefault()
	c.Description = c.DescriptionOrDefault()

	content, err := render("command.go", commandTemplate, c)
	if err != nil {
		return nil, err
	}
	return []File{{
		Path:    filepath.Join("app", "Console", "Commands", c.Type()+".go"),
		Content: content,
	}}, nil
}

// defaultSignature turns CloseInvoices into close:invoices.
//
// It splits on the capitals, which is the only structure a Go type name carries.
func defaultSignature(name string) string {
	var words []string
	start := 0
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			words = append(words, name[start:i])
			start = i
		}
	}
	words = append(words, name[start:])
	if len(words) < 2 {
		return strings.ToLower(name)
	}
	return strings.ToLower(words[0]) + ":" + strings.ToLower(strings.Join(words[1:], "-"))
}

const commandTemplate = `// Package commands holds this application's console commands. Each is a
// function that builds a console.Command over what it reaches, and the list of
// them is Commands in routes/console.go.
//
// The directory is app/Console/Commands, where people look for it. The package
// name follows Go and stays lowercase.
package commands

import (
	"context"

	"github.com/arandu-io/hesape/console"
)

// {{ .Type }} builds the ` + "`{{ .Signature }}`" + ` command, which the listing
// describes as: {{ .Description }}
//
// It is reached by listing it in Commands in routes/console.go. Nothing scans
// this directory: a command that is not in that list does not exist, and one
// that is in it with a broken signature does not build.
//
// What it reaches is a parameter, passed there from the ConsoleDeps the
// provider fills with the services it built at boot. A command that reaches
// for a global is a command no test can pin.
func {{ .Type }}() console.Command {
	return console.Command{
		Name:        {{ quote .Signature }},
		Description: {{ quote .Description }},
		// Run does the work. The arguments that followed the name are
		// o.Args(); a Signature on the command gives it named arguments and
		// options instead.
		//
		// A command runs outside a request, so there is no session and no
		// Grant from a signed-in person. Work that touches tenant data takes
		// auth.SystemGrant with the tenant named explicitly, and ` + "`aru doctor`" + `
		// checks that it is scoped.
		Run: func(ctx context.Context, o *console.IO) error {
			// arandu:begin custom
			o.Line("{{ .Signature }}: not implemented")
			return nil
			// arandu:end custom
		},
	}
}
`
