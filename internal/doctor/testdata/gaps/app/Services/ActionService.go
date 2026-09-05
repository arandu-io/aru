package services

import (
	"github.com/arandu-io/framework/security"

	policies "example.test/gaps/app/Policies"
)

// deleteReport passes a constant another package declares. The name is
// qualified, which is the same syntax a struct field uses, and only the imports
// tell the two apart.
func deleteReport(g security.Grant) error {
	return g.Check(policies.ActionDeleteReport)
}

// forward receives an action already made and hands it on. It builds nothing,
// so there is nothing here for a catalogue to be unable to read.
func forward(g security.Grant, a security.Action) error {
	return g.Check(a)
}

// convertLiteral writes the conversion at the call site. It is not a declared
// constant and it is still fixed in the source, so `aru action:list` reads it
// and the check has something to compare.
func convertLiteral(g security.Grant) error {
	return g.Check(security.Action("report.export"))
}

// localConstant declares the action inside the function it is used in. That is
// the shape being asked for, in the smallest scope it fits in.
func localConstant(g security.Grant) error {
	const archive security.Action = "report.archive"
	return g.Check(archive)
}

// splitLiteral is folded by the compiler, so the value is as fixed as a single
// quoted string and reads the same way to anything parsing the file.
func splitLiteral(g security.Grant) error {
	return g.Check(security.Action("report." + "restore"))
}
