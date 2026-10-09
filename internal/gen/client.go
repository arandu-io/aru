package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ClientSpec is one client of an external system to write.
type ClientSpec struct {
	// Vendor is the system's name as a Go type: Stripe.
	Vendor string
	// ModulePath is the project's, so the generated test imports the client.
	ModulePath string
}

// Type, Interface, Fake, Config and Status are the names the client file
// declares: StripeClient, Stripe, StripeFake, StripeConfig, StripeStatus.
func (s ClientSpec) Type() string      { return s.Vendor + "Client" }
func (s ClientSpec) Interface() string { return s.Vendor }
func (s ClientSpec) Fake() string      { return s.Vendor + "Fake" }
func (s ClientSpec) Config() string    { return s.Vendor + "Config" }
func (s ClientSpec) Status() string    { return s.Vendor + "Status" }

// Lower is the vendor in lowercase, for the prefix of an error message:
// stripe.
func (s ClientSpec) Lower() string { return strings.ToLower(s.Vendor) }

// ClientsImport is where the generated client lives.
func (s ClientSpec) ClientsImport() string { return s.ModulePath + "/app/Clients" }

// RenderClient produces app/Clients/<Vendor>Client.go, the fake beside it and
// the test that uses both.
func RenderClient(s ClientSpec) ([]File, error) {
	if !IsExportedIdentifier(s.Vendor) {
		return nil, fmt.Errorf("%q is not a Go type name", s.Vendor)
	}
	if s.ModulePath == "" {
		return nil, errModulePath
	}
	var out []File
	for _, t := range []struct{ path, tmpl string }{
		{filepath.Join("app", "Clients", s.Type()+".go"), clientTemplate},
		{filepath.Join("app", "Clients", s.Fake()+".go"), clientFakeTemplate},
		{filepath.Join("tests", "Unit", s.Type()+"_test.go"), clientTestTemplate},
	} {
		content, err := render(filepath.Base(t.path), t.tmpl, s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.path, err)
		}
		out = append(out, File{Path: t.path, Content: content})
	}
	return out, nil
}

const clientTemplate = `package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/http/client"
)

// {{.Config}} is what the {{.Vendor}} client needs, typed. bootstrap/app.go
// reads it from the configuration once and hands it over; nothing here looks a
// setting up again.
type {{.Config}} struct {
	// BaseURL is where {{.Vendor}} answers, with its scheme.
	BaseURL string
	// Token is the credential every request carries, as a bearer token.
	Token string
	// Timeout bounds each attempt. Zero leaves the client's own deadline.
	Timeout time.Duration
}

// LogValue implements slog.LogValuer, so a config passed to a log call, or
// dumped on the debug page, records where the client points and never the
// token.
func (c {{.Config}}) LogValue() slog.Value {
	return slog.GroupValue(slog.String("base_url", c.BaseURL), slog.Duration("timeout", c.Timeout))
}

// MarshalJSON writes the same fields LogValue does, for the same reason: the
// token stays out of anything encoded.
func (c {{.Config}}) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"base_url": c.BaseURL, "timeout": c.Timeout.String()})
}

// {{.Interface}} is what a service, a job or a listener depends on: the calls
// this application makes to {{.Vendor}}, and nothing else. It is small on
// purpose and declared here, beside the one implementation, so the code that
// calls {{.Vendor}} names this interface and a test hands it {{.Fake}}.
type {{.Interface}} interface {
	Status(ctx context.Context) ({{.Status}}, error)

	// arandu:begin custom
	// The next call to {{.Vendor}} goes here, and on the client and the fake.
	// arandu:end custom
}

// {{.Status}} is what Status answers: a type of this client's own, so the code
// that calls it never reads {{.Vendor}}'s wire format.
type {{.Status}} struct {
	OK bool ` + "`" + `json:"ok"` + "`" + `
}

// {{.Type}} talks to {{.Vendor}} over hesape/http/client, which is the only way
// a request leaves this application: every attempt carries a deadline, reads a
// bounded body, and refuses an address inside the network.
//
// It holds no Grant, no model and no session. It is reached by the service
// that decided to call it -- after the policy -- and answers with its own
// types, so what {{.Vendor}} changes on its side changes this file and no other.
type {{.Type}} struct {
	config {{.Config}}
	http   *client.Factory
}

// New{{.Type}} builds the client with its configuration and the factory its
// requests are made from: client.NewFactory(nil) in bootstrap/app.go, and the
// same factory faked in a test that wants to see the request.
func New{{.Type}}(config {{.Config}}, http *client.Factory) *{{.Type}} {
	return &{{.Type}}{config: config, http: http}
}

// Compile-time proof that the client is what the code calling it depends on.
var _ {{.Interface}} = (*{{.Type}})(nil)

// Status asks {{.Vendor}} whether it is answering.
func (c *{{.Type}}) Status(ctx context.Context) ({{.Status}}, error) {
	res, err := c.request().Get(ctx, "/status", nil)
	if err != nil {
		return {{.Status}}{}, fmt.Errorf("{{.Lower}}: %w", err)
	}
	if res.Failed() {
		return {{.Status}}{}, fmt.Errorf("{{.Lower}}: status answered %d", res.Status())
	}
	var out {{.Status}}
	if err := res.Object(&out); err != nil {
		return {{.Status}}{}, fmt.Errorf("{{.Lower}}: reading the status: %w", err)
	}
	return out, nil
}

// request is every call's starting point: the base address, the credential and
// the deadline, set once rather than at each call.
func (c *{{.Type}}) request() *client.PendingRequest {
	r := c.http.CreatePendingRequest().BaseURL(c.config.BaseURL).AcceptJSON()
	if c.config.Token != "" {
		r = r.WithToken(c.config.Token, "Bearer")
	}
	if c.config.Timeout > 0 {
		r = r.Timeout(c.config.Timeout)
	}
	return r
}

// arandu:begin custom
// arandu:end custom
`

const clientFakeTemplate = `package clients

import "context"

// {{.Fake}} is {{.Interface}} for the tests: it answers what it was told to,
// records what it was asked, and never reaches the network. A service under
// test is built with one in place of {{.Type}}.
type {{.Fake}} struct {
	// StatusAnswer is what Status answers.
	StatusAnswer {{.Status}}
	// Err, when set, is what every call answers instead.
	Err error
	// Calls are the methods called, in order.
	Calls []string
}

// Compile-time proof that the fake stands in for the client.
var _ {{.Interface}} = (*{{.Fake}})(nil)

// Status records the call and answers StatusAnswer, or Err.
func (f *{{.Fake}}) Status(ctx context.Context) ({{.Status}}, error) {
	f.Calls = append(f.Calls, "Status")
	if f.Err != nil {
		return {{.Status}}{}, f.Err
	}
	return f.StatusAnswer, nil
}

// arandu:begin custom
// arandu:end custom
`

const clientTestTemplate = `package unit_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/arandu-io/hesape/http/client"

	clients "{{.ClientsImport}}"
)

// TestThe{{.Fake}}StandsInForTheClient is the shape every test of code that
// calls {{.Vendor}} takes: the code depends on clients.{{.Interface}}, and the
// test hands it the fake and reads what was asked.
func TestThe{{.Fake}}StandsInForTheClient(t *testing.T) {
	fake := &clients.{{.Fake}}{StatusAnswer: clients.{{.Status}}{OK: true}}
	var vendor clients.{{.Interface}} = fake

	status, err := vendor.Status(context.Background())
	if err != nil || !status.OK {
		t.Fatalf("Status = %+v, %v: want the answer the fake was given", status, err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0] != "Status" {
		t.Errorf("the fake recorded %v, want one Status", fake.Calls)
	}
}

// TestThe{{.Type}}SendsWhatItIsConfiguredWith runs the real client against a
// faked factory: the request it builds is read, and nothing leaves the
// process.
func TestThe{{.Type}}SendsWhatItIsConfiguredWith(t *testing.T) {
	var sent *http.Request
	factory := client.NewFactory(nil).Fake(func(r *http.Request) (*http.Response, error) {
		sent = r
		return client.NewResponseFromBytes(http.StatusOK, []byte(` + "`" + `{"ok":true}` + "`" + `), nil).HTTPResponse(), nil
	})
	c := clients.New{{.Type}}(clients.{{.Config}}{BaseURL: "https://{{.Lower}}.example.test", Token: "secret"}, factory)

	status, err := c.Status(context.Background())
	if err != nil || !status.OK {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if sent == nil || sent.URL.Path != "/status" || sent.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("the request was not the one configured: %+v", sent)
	}
}
// arandu:begin custom
// arandu:end custom
`
