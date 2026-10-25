package requests

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/arandu-io/hesape/validation"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

// StorePerson is the input contract of a sign-up, and it checks every field by
// hand: each of them has a rule that says the same, and no Rules declares it.
type StorePerson struct {
	Name  string `form:"name"`
	Email string `form:"email"`
	Age   string `form:"age"`
	Kind  string `form:"kind"`
	Bio   string `form:"bio"`
}

// Validate reports the errors per field.
func (r StorePerson) Validate() validation.Errors {
	e := validation.Errors{}
	if strings.TrimSpace(r.Name) == "" {
		e.Add("name", "is required")
	}
	if !emailPattern.MatchString(r.Email) {
		e.Add("email", "is not an address")
	}
	if _, err := strconv.Atoi(r.Age); err != nil {
		e.Add("age", "is not a number")
	}
	if !slices.Contains([]string{"adult", "minor"}, r.Kind) {
		e.Add("kind", "is not a kind")
	}
	if len(r.Bio) > 500 {
		e.Add("bio", "is too long")
	}
	validation.MaxLen(e, "name", r.Name, 255)
	return e
}
