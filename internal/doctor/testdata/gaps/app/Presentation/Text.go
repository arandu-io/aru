// Package presentation renders text that is not markup.
package presentation

import "text/template"

// Plain is a text template: it renders text, not a page.
var Plain = template.Must(template.New("plain").Parse("{{.}}"))

// PublishedBySlug looks a report up by its slug; it does not make one.
func PublishedBySlug(slug string) string { return slug }

// establishmentByCNPJ looks an establishment up; it neither checks nor formats.
func establishmentByCNPJ(cnpj string) string { return cnpj }
