package requests

import (
	"github.com/arandu-io/framework/validation"

	enums "example.test/gaps/app/Enums"
)

// StoreReport is the input contract of creation.
type StoreReport struct {
	Title  string
	Format enums.ReportFormat
	State  enums.ReportState
	Colour string
}

// Store is the rule set, and every line of it is a near miss.
//
// format lists cases beside an integer-backed type: the list is written in
// shown spellings and the column holds numbers, so nobody can say from the
// source whether it agrees.
//
// state is the derived form -- `enum` with nothing after it reads the cases off
// the type, which is what the rule asks for.
//
// colour is a plain string with no type behind it, so the list is the only set
// there is and dropping it would leave nothing to decide against.
var Store = validation.MustCompile(validation.Rules{
	"title":  "required|max:255",
	"format": "required|enum:csv,pdf,json",
	"state":  "required|enum",
	"colour": "required|enum:red,green,blue",
})

// notARuleSet is a map of strings that is not a rule set, keyed by something
// that is not an input name.
var notARuleSet = map[string]string{
	"status": "required|enum:queued,ready,archived",
}
