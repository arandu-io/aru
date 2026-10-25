package requests

import (
	"regexp"
	"strconv"
	"time"

	"github.com/arandu-io/hesape/validation"
)

var platePattern = regexp.MustCompile(`^[A-Z]{3}[0-9][A-Z0-9][0-9]{2}$`)

// StoreSchedule is the input contract of a booking, and every check in its
// Validate is a rule of the domain that no string of rules says.
type StoreSchedule struct {
	Start    time.Time `form:"start"`
	End      time.Time `form:"end"`
	Plate    string    `form:"plate"`
	Quantity string    `form:"quantity"`
	Seats    string    `form:"seats"`
}

// Validate reports the errors per field.
//
// end is compared with start, which is two fields; plate is a pattern of the
// domain the doctor cannot name; quantity is parsed and the number is then
// compared, so the parse is not the whole check; seats is compared with a
// literal that is no length.
func (r StoreSchedule) Validate() validation.Errors {
	e := validation.Errors{}
	if r.End.Before(r.Start) {
		e.Add("end", "must come after the start")
	}
	if !platePattern.MatchString(r.Plate) {
		e.Add("plate", "is not a plate")
	}
	if quantity, err := strconv.Atoi(r.Quantity); err == nil && quantity > 40 {
		e.Add("quantity", "is more than a van holds")
	}
	if r.Seats == "13" {
		e.Add("seats", "is unlucky")
	}
	return e
}
