package gen_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestAFieldNameKeepsItsInitialisms is the one inflector a field name goes
// through. It wrote user_id as UserId, which golint and staticcheck report in
// every project that declared one, and which nobody writes by hand.
func TestAFieldNameKeepsItsInitialisms(t *testing.T) {
	for name, want := range map[string]string{
		"user_id":         "UserID",
		"api_url":         "APIURL",
		"id":              "ID",
		"http_server_url": "HTTPServerURL",
		"external_uuid":   "ExternalUUID",
		"full_name":       "FullName",
		"ids":             "Ids",
		"invoice_line2":   "InvoiceLine2",
		"identity":        "Identity",
	} {
		if got := (gen.Field{Name: name}).GoName(); got != want {
			t.Errorf("%s names the field %s, want %s", name, got, want)
		}
	}
}

// TestTheGeneratorsNameAFieldAlike: the event, the job, the model and the
// request name a field through the same inflector, so a payload and the row it
// came from carry one name for one column.
func TestTheGeneratorsNameAFieldAlike(t *testing.T) {
	fields := []gen.Field{{Name: "user_id", Type: gen.TypeUUID, Required: true}, {Name: "api_url", Type: gen.TypeString}}

	event, err := gen.RenderEvent(gen.EventSpec{
		Type: "UserLinked", Aggregate: "user", EventName: "user.linked", ModulePath: "example.test/project", Fields: fields,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := gen.RenderJob(gen.JobSpec{
		Type: "LinkUser", EventName: "user.link", ModulePath: "example.test/project", Fields: fields,
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := gen.GenerateModel(gen.Module{
		Name: "link", Fields: fields, ModulePath: "example.test/project", Date: "2026_10_09",
	}, gen.ModelParts{Request: true})
	if err != nil {
		t.Fatal(err)
	}

	sources := map[string]string{"event": string(event.Content), "job": string(job.Content)}
	for _, f := range module {
		sources[f.Path] = string(f.Content)
	}
	for _, path := range []string{"event", "job", "app/Models/Link.go", "app/Http/Requests/LinkRequest.go"} {
		for _, want := range []string{"UserID string", "APIURL string"} {
			if !strings.Contains(strings.Join(strings.Fields(sources[path]), " "), want) {
				t.Errorf("%s does not declare %s:\n%s", path, want, sources[path])
			}
		}
		if strings.Contains(sources[path], "UserId") || strings.Contains(sources[path], "ApiUrl") {
			t.Errorf("%s spells an initialism in mixed case:\n%s", path, sources[path])
		}
	}
}

// TestAServiceCopiesAFieldUnderItsOwnName: a service reads the model and the
// request back off their structs, and a name of two initialisms does not
// survive snake case -- APIURL is apiurl. The field keeps the name it was read
// under, so fill copies APIURL and not a field called Apiurl.
func TestAServiceCopiesAFieldUnderItsOwnName(t *testing.T) {
	both := []gen.FactoryField{{GoName: "APIURL", GoType: "string"}, {GoName: "UserID", GoType: "string"}}
	for _, f := range gen.ServiceFields(both, both) {
		if f.GoName() != "APIURL" && f.GoName() != "UserID" {
			t.Errorf("a field read as %s is copied as %s", f.Name, f.GoName())
		}
	}
}
