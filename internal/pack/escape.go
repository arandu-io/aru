package pack

import (
	"bytes"
	"encoding/xml"
	"html"
	"text/template"
)

// Values a project chooses reach five markup documents, and none of them is
// HTML except one.
//
// An application name, a bundle identifier and a URL scheme are written by
// whoever owns the project, and they end up inside an Android manifest, two
// property lists, a Windows assembly manifest and the page a browser build
// loads. The first four are XML and the last is HTML, and the escape each one
// needs is different: `&` alone makes an XML document unparseable, so a name as
// ordinary as "Faturas & Cobranças" produced a package no tool would read.
//
// The templates are text, not markup-aware, so the escape is named at the point
// of insertion rather than inferred. That is deliberate: a reader of the
// template sees which escape applies, and a value inserted without one is
// visible as an omission instead of looking like every other field.

// markup is the function map every markup template in this package parses with.
//
// It has one entry per document language, and no default: a template that
// inserts a value without naming an escape does not compile a wrong escape by
// accident, it inserts the value raw, which is what the tests for these
// functions exist to catch.
var markup = template.FuncMap{
	"xml":  xmlText,
	"html": html.EscapeString,
}

// xmlText escapes a value for use as XML text or as the content of a quoted
// attribute.
//
// It goes through encoding/xml rather than a hand-written replacer because the
// set is larger than the obvious three: a tab, a newline and a carriage return
// inside an attribute are re-read as a single space unless they are escaped as
// character references, which silently corrupts a name that was pasted with one
// in it.
func xmlText(value string) string {
	var out bytes.Buffer
	// EscapeText fails only when the writer fails, and a bytes.Buffer does not.
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
