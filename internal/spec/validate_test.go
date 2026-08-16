package spec_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/spec"
)

// TestANameThatWouldNotCompileIsRefused collects the names that would otherwise
// reach the generator and fail somewhere else -- in the SQL engine, in the Go
// compiler, or as "bug in the template", which sends the author looking in the
// wrong file entirely.
func TestANameThatWouldNotCompileIsRefused(t *testing.T) {
	cases := []struct {
		what   string
		module spec.Module
		says   string
	}{
		{
			what:   "a module named after a SQL keyword",
			module: moduleNamed("order"),
			says:   "reserved in SQL",
		},
		{
			what:   "a module named after a Go keyword",
			module: moduleNamed("range"),
			says:   "Go keyword",
		},
		{
			what:   "a module named after something the generator writes",
			module: moduleNamed("service"),
			says:   "collides with an identifier this generator writes",
		},
		{
			what:   "a field named after a SQL keyword",
			module: moduleWithField(spec.Field{Name: "order", Type: "string"}),
			says:   "reserved in SQL",
		},
		{
			what:   "a field named after a Go keyword",
			module: moduleWithField(spec.Field{Name: "type", Type: "string"}),
			says:   "Go keyword",
		},
		{
			what:   "a required bool, which cannot mean anything",
			module: moduleWithField(spec.Field{Name: "active", Type: "bool", Required: true}),
			says:   "false is an answer, not an absence",
		},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			err := c.module.Validate()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the error does not say why:\n%v", err)
			}
		})
	}
}

// TestTwoColumnsCannotShareOneGoField: full_name and full__name are two valid
// column names and one struct field, because the underscore is a separator and
// two of them separate the same way. The Go compiler reported "FullName
// redeclared" against a file the author never opened.
func TestTwoColumnsCannotShareOneGoField(t *testing.T) {
	m := moduleNamed("invoice")
	m.Fields = []spec.Field{
		{Name: "full_name", Type: "string"},
		{Name: "full__name", Type: "string"},
	}

	err := m.Validate()
	if err == nil {
		t.Fatal("accepted two columns that become one Go field")
	}
	// The message has to name both, or the author fixes the wrong one.
	for _, want := range []string{"full_name", "full__name", "FullName"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// TestAnOrdinaryModuleStillPasses guards the checks above from becoming a wall.
func TestAnOrdinaryModuleStillPasses(t *testing.T) {
	m := moduleNamed("purchase_order")
	m.Fields = []spec.Field{
		{Name: "reference", Type: "string", Required: true, Unique: true},
		{Name: "total", Type: "money", Required: true},
		{Name: "approved", Type: "bool"},
		{Name: "ordered_on", Type: "date"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("a plain module was refused: %v", err)
	}
}

func moduleNamed(name string) spec.Module {
	return spec.Module{
		Version: spec.Version,
		Name:    name,
		Fields:  []spec.Field{{Name: "reference", Type: "string"}},
	}
}

func moduleWithField(f spec.Field) spec.Module {
	m := moduleNamed("invoice")
	m.Fields = []spec.Field{f}
	return m
}
