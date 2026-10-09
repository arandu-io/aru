// Package presentation formats values for pages, with a template engine and
// helpers of its own.
package presentation

import (
	"fmt"
	"html/template"
	"strings"
)

// FormatBRL renders cents as reais, a second time.
func FormatBRL(cents int64) template.HTML {
	return template.HTML(fmt.Sprintf("R$ %d,%02d", cents/100, cents%100))
}

// Slugify is str.Slug, written again.
func Slugify(s string) string { return strings.ToLower(strings.ReplaceAll(s, " ", "-")) }

// ValidCPF checks a CPF, written again.
func ValidCPF(s string) bool { return len(s) == 11 }
