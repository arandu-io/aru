package gen

import (
	"fmt"
	"path/filepath"
)

// GenerateService produces app/Services/<Entity>Service.go with one use case,
// Create, and the unit test that proves it asks before it writes.
//
// It renders the blocks `aru make:module` renders its service from, so the
// method is the same one: validate, Authorize, Grant, Model. What the module
// adds -- Get, List, Update, Delete -- is the rest of a resource, and a service
// written for one use case does not carry four it was not asked for.
//
// The Module's fields are the ones fill copies from the request onto the
// entity: ServiceFields says which, from the two structs as they stand.
func GenerateService(m Module) ([]File, error) {
	if m.Name == "" || m.ModulePath == "" {
		return nil, fmt.Errorf("a service needs an entity and the project module path")
	}
	service, err := render(m.ServiceType()+".go", serviceStubTemplate+serviceBlocks, m)
	if err != nil {
		return nil, err
	}
	test, err := render(m.ServiceType()+"_test.go", serviceTestTemplate, m)
	if err != nil {
		return nil, err
	}
	return []File{
		{Path: filepath.Join("app", "Services", m.ServiceType()+".go"), Content: service},
		{Path: filepath.Join("tests", "Unit", m.ServiceType()+"_test.go"), Content: test},
	}, nil
}

// ServiceFields are the fields fill copies: the ones the request and the entity
// both declare, under one name and one Go type.
//
// It reads the two structs rather than a specification because a service is
// written next to a model and a request that already exist, and either may
// have been changed by hand since it was generated. A field only one of them
// has, or that they type differently, is left to the custom block -- a copy
// the generator guessed at would be a conversion nobody wrote.
func ServiceFields(model, request []FactoryField) []Field {
	byName := make(map[string]string, len(model))
	for _, f := range model {
		byName[f.GoName] = f.GoType
	}
	var out []Field
	for _, f := range request {
		t, kind := byName[f.GoName], goKinds[f.GoType]
		if t != f.GoType || kind == "" {
			continue
		}
		out = append(out, Field{Name: Normalize(f.GoName), Type: kind})
	}
	return out
}

// goKinds maps a Go type back to the field type whose fill copies it as it is.
// The text kinds all read as TypeString and a time as TypeTimestamp: what fill
// needs to know is how to assign, and every one of these assigns unchanged.
var goKinds = map[string]Type{
	"string":    TypeString,
	"int64":     TypeInt,
	"float64":   TypeDecimal,
	"bool":      TypeBool,
	"time.Time": TypeTimestamp,
}

const serviceStubTemplate = `package services

import (
	"context"
{{- if .HasEmail}}
	"strings"
{{- end}}

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/log"

	models "{{.ModelsImport}}"
	policies "{{.PoliciesImport}}"
	requests "{{.RequestsImport}}"
)

{{template "serviceStruct" .}}{{template "serviceCreate" .}}{{template "serviceFill" .}}// arandu:begin custom
// The next use case goes here, in the same shape: (ctx, actor auth.Subject, in
// requests.X) or an id, validated, authorized, then the Model with the Grant.
// A rule about one {{.Name}} -- a transition, an invariant -- is a method of the
// entity in app/Models/{{.Entity}}.go, and this service calls it.
// arandu:end custom
`

const serviceTestTemplate = `package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/validation"

	requests "{{.RequestsImport}}"
	services "{{.ServicesImport}}"
)

// TestThe{{.ServiceType}}AsksBeforeItWrites needs no database: Create validates
// and then authorizes before it reaches the Model, so the nil handle below turns
// a write that skipped either into a panic here rather than a row in
// production. Nobody is asking, so the answer is a rejected form or a refusal,
// and never a record.
func TestThe{{.ServiceType}}AsksBeforeItWrites(t *testing.T) {
	svc := services.New{{.ServiceType}}(nil)
	var anonymous auth.Subject

	created, err := svc.Create(context.Background(), anonymous, requests.{{.Request}}{})

	var invalid validation.Errors
	if created != nil || (!errors.Is(err, auth.ErrForbidden) && !errors.As(err, &invalid)) {
		t.Fatalf("Create = %v, %v: want no record and a validation error or ErrForbidden", created, err)
	}
}
// arandu:begin custom
// Tests for the use cases you add go here, and survive regeneration.
// arandu:end custom
`
