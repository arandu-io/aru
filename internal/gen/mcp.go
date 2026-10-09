package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MCPModule is the module an application exposes itself to an assistant with,
// and MCPRelease the release the generated tools, resources and prompts are
// compiled against. A project that does not require it yet is told to take
// this one; nothing here edits a go.mod.
const (
	MCPModule  = "github.com/arandu-io/mcp"
	MCPRelease = "v0.4.0"
)

// MCPKind is which of the three things a client reaches an application through
// is being written.
type MCPKind string

// The three, and there is no fourth: the protocol has tools, resources and
// prompts.
const (
	MCPTool     MCPKind = "tool"
	MCPResource MCPKind = "resource"
	MCPPrompt   MCPKind = "prompt"
)

// MCPSpec is one tool, resource or prompt to write in app/Mcp.
type MCPSpec struct {
	// Type is the exported Go type: ShowInvoice.
	Type string
	// Kind picks what it is.
	Kind MCPKind
	// Service is the entity whose service a tool or a resource calls: Invoice.
	// A prompt calls none.
	Service string
	// ModulePath is the project's, for the generated imports.
	ModulePath string
}

// Name is what a client calls it by: show_invoice. Lower case with
// underscores, which every client displays without quoting.
func (s MCPSpec) Name() string { return Normalize(s.Type) }

// URI is where a client reads a resource: app://invoices.
func (s MCPSpec) URI() string { return "app://" + Kebab(s.Type) }

// Description is what the model reads to decide whether to use it, and it
// starts as the name in words; it is the string most worth rewriting.
func (s MCPSpec) Description() string { return Humanise(s.Type) + "." }

// ServiceType is the service a tool or a resource holds: InvoiceService.
func (s MCPSpec) ServiceType() string { return Exported(s.Service) + "Service" }

// Entity is the service's entity in words, for the doc comments: invoice.
func (s MCPSpec) Entity() string { return strings.ReplaceAll(Normalize(s.Service), "_", " ") }

// IsTool, IsResource and IsPrompt pick the template's branch.
func (s MCPSpec) IsTool() bool     { return s.Kind == MCPTool }
func (s MCPSpec) IsResource() bool { return s.Kind == MCPResource }
func (s MCPSpec) IsPrompt() bool   { return s.Kind == MCPPrompt }

// ServicesImport and MCPImport are where the service and the generated type
// live.
func (s MCPSpec) ServicesImport() string { return s.ModulePath + "/app/Services" }
func (s MCPSpec) MCPImport() string      { return s.ModulePath + "/app/Mcp" }

// Constructor is how bootstrap/app.go builds it: NewShowInvoice(svc) for a
// tool or a resource, the literal for a prompt.
func (s MCPSpec) Constructor(svc string) string {
	if s.IsPrompt() {
		return "appmcp." + s.Type + "{}"
	}
	return "appmcp.New" + s.Type + "(" + svc + ")"
}

// RenderMCP produces app/Mcp/<Type>.go and its test.
func RenderMCP(s MCPSpec) ([]File, error) {
	if !IsExportedIdentifier(s.Type) {
		return nil, fmt.Errorf("%q is not a Go type name", s.Type)
	}
	if s.ModulePath == "" {
		return nil, errModulePath
	}
	switch s.Kind {
	case MCPTool, MCPResource:
		if s.Service == "" {
			return nil, fmt.Errorf("a %s calls a service, and none was named", s.Kind)
		}
	case MCPPrompt:
	default:
		return nil, fmt.Errorf("unknown kind %q", s.Kind)
	}
	body, err := render(s.Type+".go", mcpTemplate, s)
	if err != nil {
		return nil, err
	}
	test, err := render(s.Type+"_test.go", mcpTestTemplate, s)
	if err != nil {
		return nil, err
	}
	return []File{
		{Path: filepath.Join("app", "Mcp", s.Type+".go"), Content: body},
		{Path: filepath.Join("tests", "Unit", s.Type+"MCP_test.go"), Content: test},
	}, nil
}

const mcpTemplate = `package mcp

import (
	"context"

{{if .IsResource}}	"github.com/arandu-io/hesape/auth"
{{end}}	"github.com/arandu-io/mcp"
{{- if not .IsPrompt}}

	services "{{.ServicesImport}}"
{{- end}}
)
{{if .IsTool}}
// {{.Type}} is a tool an assistant can call.
//
// It is written the way a controller is: it reads its arguments, calls the
// service with the subject the request carried, and answers. The service asks
// the policy and spends the Grant; this type never reaches a model, a
// repository or a client, because a tool that queries the database itself is
// the largest hole an application can hand a language model.
type {{.Type}} struct {
	svc *services.{{.ServiceType}}
}

// New{{.Type}} builds the tool with the service it calls.
func New{{.Type}}(svc *services.{{.ServiceType}}) {{.Type}} {
	return {{.Type}}{svc: svc}
}

// Compile-time proof that the server takes it.
var _ mcp.Tool = {{.Type}}{}

// Name is what the client calls it by.
func ({{.Type}}) Name() string { return {{quote .Name}} }

// Description is what the model reads to decide whether to call it: the single
// string here most worth rewriting, because a model that calls the wrong tool
// was told the wrong thing.
func ({{.Type}}) Description() string { return {{quote .Description}} }

// Schema declares the arguments. A call without the id is refused before
// Handle runs, so Handle reads it without checking.
func ({{.Type}}) Schema() mcp.Schema {
	return mcp.Object(
		mcp.String("id", "The {{.Entity}} to read").Required(),
	)
}

// Handle calls the service as who is asking. A refusal is returned as the
// error, and the server answers it as a failure the model reads -- never as an
// empty result it would take for "there is nothing there".
func (t {{.Type}}) Handle(ctx context.Context, r mcp.Request) (mcp.Response, error) {
	id, _ := r.String("id")
	found, err := t.svc.Get(ctx, r.Subject(), id)
	if err != nil {
		return mcp.Response{}, err
	}
	// arandu:begin custom
	// Answer with a JSON Resource -- mcp.JSON(resources.New...Resource(found))
	// -- for structured data, the same document a controller answers with.
	return mcp.Text("{{.Entity}} %s", found.ID), nil
	// arandu:end custom
}
{{end}}{{if .IsResource}}
// {{.Type}} is a resource an assistant can read, at {{.URI}}.
//
// It is an MCP resource -- a document a client asks for by address -- and not
// a JSON Resource. It reads through the service as the subject the request
// carried, so the policy that decides about a controller decides about it.
type {{.Type}} struct {
	svc *services.{{.ServiceType}}
}

// New{{.Type}} builds the resource with the service it reads through.
func New{{.Type}}(svc *services.{{.ServiceType}}) {{.Type}} {
	return {{.Type}}{svc: svc}
}

// Compile-time proof that the server takes it.
var _ mcp.Resource = {{.Type}}{}

// URI is where a client reads it.
func ({{.Type}}) URI() string { return {{quote .URI}} }

// Name and Description are what the client lists it as.
func ({{.Type}}) Name() string        { return {{quote .Name}} }
func ({{.Type}}) Description() string { return {{quote .Description}} }

// MimeType is what the content is.
func ({{.Type}}) MimeType() string { return "text/plain" }

// Read lists the first page through the service, as who is asking.
func (res {{.Type}}) Read(ctx context.Context, s auth.Subject) (mcp.Response, error) {
	found, _, err := res.svc.List(ctx, s, 1)
	if err != nil {
		return mcp.Response{}, err
	}
	// arandu:begin custom
	return mcp.Text("%d {{.Entity}} records on the first page", len(found)), nil
	// arandu:end custom
}
{{end}}{{if .IsPrompt}}
// {{.Type}} is a conversation this application knows how to start: the client
// fills the arguments in, and Render builds the messages.
//
// It reads no data. A prompt that needs a record is a prompt whose client
// calls a tool for it, through the policy, rather than one handed the record
// here.
type {{.Type}} struct{}

// Compile-time proof that the server takes it.
var _ mcp.Prompt = {{.Type}}{}

// Name is what the client calls it by.
func ({{.Type}}) Name() string { return {{quote .Name}} }

// Description is what the client lists it as.
func ({{.Type}}) Description() string { return {{quote .Description}} }

// Arguments are what the client fills in. A required one that is missing is
// refused before Render runs.
func ({{.Type}}) Arguments() []mcp.Argument {
	return []mcp.Argument{
		{Name: "topic", Description: "What the conversation is about", Required: true},
	}
}

// Render builds the messages from the arguments.
func ({{.Type}}) Render(ctx context.Context, r mcp.Request) ([]mcp.Message, error) {
	topic, _ := r.String("topic")
	// arandu:begin custom
	return []mcp.Message{
		mcp.User("Help me with " + topic + "."),
	}, nil
	// arandu:end custom
}
{{end}}`

const mcpTestTemplate = `package unit_test

import (
	"context"
	"testing"

{{if not .IsPrompt}}	"github.com/arandu-io/hesape/auth"
{{end}}	"github.com/arandu-io/mcp"

	appmcp "{{.MCPImport}}"
{{- if not .IsPrompt}}
	services "{{.ServicesImport}}"
{{- end}}
)
{{if .IsTool}}
// TestThe{{.Type}}ToolGoesThroughThePolicy calls the tool through a server, as
// a client does, with nobody signed in. The service refuses before it reaches
// the database -- the nil handle below would panic otherwise -- and the server
// answers the refusal as a failure, never as a result.
func TestThe{{.Type}}ToolGoesThroughThePolicy(t *testing.T) {
	server := &mcp.Server{Name: "test", Version: "0", Tools: []mcp.Tool{
		appmcp.New{{.Type}}(services.New{{.ServiceType}}(nil)),
	}}
	if err := server.Validate(); err != nil {
		t.Fatalf("the server refuses the tool: %v", err)
	}

	var anonymous auth.Subject
	if got := server.Call(context.Background(), anonymous, {{quote .Name}}, map[string]any{"id": "1"}); !got.IsError {
		t.Errorf("an anonymous call was answered as a result: %q", got.Text)
	}
	if got := server.Call(context.Background(), anonymous, {{quote .Name}}, nil); !got.IsError {
		t.Errorf("a call without the id was answered as a result: %q", got.Text)
	}
}
{{end}}{{if .IsResource}}
// TestThe{{.Type}}ResourceGoesThroughThePolicy reads the resource through a
// server with nobody signed in: the service refuses before the database, and
// the server answers the refusal as a failure.
func TestThe{{.Type}}ResourceGoesThroughThePolicy(t *testing.T) {
	server := &mcp.Server{Name: "test", Version: "0", Resources: []mcp.Resource{
		appmcp.New{{.Type}}(services.New{{.ServiceType}}(nil)),
	}}
	if err := server.Validate(); err != nil {
		t.Fatalf("the server refuses the resource: %v", err)
	}

	var anonymous auth.Subject
	if got := server.Read(context.Background(), anonymous, {{quote .URI}}); !got.IsError {
		t.Errorf("an anonymous read was answered as a result: %q", got.Text)
	}
}
{{end}}{{if .IsPrompt}}
// TestThe{{.Type}}PromptRendersItsArguments renders the prompt with its
// arguments and requires a message built from them.
func TestThe{{.Type}}PromptRendersItsArguments(t *testing.T) {
	server := &mcp.Server{Name: "test", Version: "0", Prompts: []mcp.Prompt{appmcp.{{.Type}}{}}}
	if err := server.Validate(); err != nil {
		t.Fatalf("the server refuses the prompt: %v", err)
	}

	messages, err := appmcp.{{.Type}}{}.Render(context.Background(), mcp.Request{Arguments: map[string]any{"topic": "the invoices"}})
	if err != nil || len(messages) == 0 {
		t.Fatalf("Render = %v, %v: want at least one message", messages, err)
	}
}
{{end}}// arandu:begin custom
// arandu:end custom
`
