package spec_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/spec"
)

// TestAnUnquotedColonSaysWhatToDo is the failure a real measurement found.
//
// Ten models were given only the schema and asked for a specification. Eight
// passed. Both failures were the same line: a description written as a sentence,
// with a colon in it, unquoted -- which ends the YAML value and makes the parser
// read the rest as a nested key.
//
// What the library says is "mapping values are not allowed in this context",
// which names a YAML concept and no action. Anybody writing a sentence in
// Portuguese or English hits this, and the message has to carry the fix.
func TestAnUnquotedColonSaysWhatToDo(t *testing.T) {
	document := `version: "1"
name: appointment
description: A medical appointment: which patient sees which doctor.
fields:
  - {name: notes, type: text}
`

	_, err := spec.Parse([]byte(document), "appointment.yaml")
	if err == nil {
		t.Fatal("an unquoted colon was accepted")
	}

	message := err.Error()
	for _, want := range []string{
		"line 3",           // where
		"has to be quoted", // what
		`description: "A medical appointment: which patient sees which doctor."`, // the fix, ready to paste
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the error does not carry %q:\n%s", want, message)
		}
	}
}

// TestAQuotedColonIsFine: the fix the message suggests has to actually work.
func TestAQuotedColonIsFine(t *testing.T) {
	document := `version: "1"
name: appointment
description: "A medical appointment: which patient sees which doctor."
fields:
  - {name: notes, type: text}
`

	m, err := spec.Parse([]byte(document), "appointment.yaml")
	if err != nil {
		t.Fatalf("the quoted form was rejected: %v", err)
	}
	if !strings.Contains(m.Description, ":") {
		t.Errorf("the colon did not survive: %q", m.Description)
	}
}

// TestTheSchemaWarnsBeforeTheErrorHappens: the message above is the second line
// of defence. The first is the schema, which is what a model reads.
func TestTheSchemaWarnsBeforeTheErrorHappens(t *testing.T) {
	body, err := spec.Schema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "QUOTE IT if the sentence contains a colon") {
		t.Error("the schema does not warn about the colon, and a model has no way to know")
	}
}
