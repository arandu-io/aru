package services

import (
	"fmt"
	"strings"

	"github.com/arandu-io/framework/security"
)

// authorizeVerb builds the action out of what it was handed.
//
// It reads as a tidy way to avoid five nearly identical methods, and it is how
// an action that exists nowhere in the source reaches Grant.Check.
func authorizeVerb(g security.Grant, verb string) error {
	return g.Check(security.Action("billing." + verb))
}

// authorizeFormatted is the same act spelled with a formatter.
func authorizeFormatted(g security.Grant, resource, verb string) error {
	return g.Check(security.Action(fmt.Sprintf("%s.%s", resource, verb)))
}

// authorizeFromAMap looks the action up in a table nobody can read from the
// call site.
var verbs = map[string]string{"read": "billing.view", "write": "billing.update"}

func authorizeFromAMap(g security.Grant, key string) error {
	return g.Check(security.Action(verbs[key]))
}

// authorizeDeclared writes the type on the left of the equals sign instead of
// around the value, which is the same construction with the conversion moved.
func authorizeDeclared(g security.Grant, verb string) error {
	var wanted security.Action = "billing." + strings.ToLower(verb)
	return g.Check(wanted)
}
