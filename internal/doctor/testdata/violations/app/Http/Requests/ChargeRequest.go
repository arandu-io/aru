package requests

import (
	"github.com/arandu-io/framework/validation"

	enums "example.test/p/app/Enums"
)

// StoreCharge is the input contract of creation.
type StoreCharge struct {
	Number string
	Status enums.ChargeStatus
	Kind   enums.ChargeKind
}

// Store is the rule set, compiled once at boot.
//
// The list beside `enum` is the second copy of a set the type already holds.
// status has already drifted -- "voided" was renamed to "refunded" in the type
// and nothing here noticed -- and kind is the same copy on the day it still
// agrees, which is the only day it ever looks correct.
var Store = validation.MustCompile(validation.Rules{
	"number": "required|max:255",
	"status": "required|enum:draft,settled,voided",
	"kind":   "required|enum:one_off,recurring",
})
