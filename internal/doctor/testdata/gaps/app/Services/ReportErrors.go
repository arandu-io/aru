package services

import (
	"errors"
	"fmt"
)

// The refusals below are prose that happens to use the words a statement is
// made of. Each one was read as SQL assembled with fmt.Sprintf, because the
// rule asked whether " WHERE " or "UPDATE " appeared anywhere in the format
// string -- and an English sentence about a report says "where" and "update"
// as readily as a query does. None of them reaches a database.

// reportNotReadable is the shape that was reported: an error message with
// "where" in it.
func reportNotReadable(id, state string) error {
	return errors.New(fmt.Sprintf("report %s is in a state where it cannot be read: %s", id, state))
}

// reportMoved says "update" and "from", and names no table.
func reportMoved(id, from string) string {
	return fmt.Sprintf("cannot update report %s: it was moved from %s", id, from)
}

// reportNotDeleted says "delete", "from" and "where", in that order.
func reportNotDeleted(id string) string {
	return fmt.Sprintf("could not delete report %s from the archive where it was kept", id)
}

// reportUnscheduled is the same sentence handed to fmt.Errorf, which is how an
// error is usually written, and which the rule never had a reason to read.
func reportUnscheduled(id string) error {
	return fmt.Errorf("report %s has no schedule where one is required", id)
}
