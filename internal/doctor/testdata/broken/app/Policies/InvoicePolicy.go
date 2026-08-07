package policies

// This file does not parse: the brace is never closed. It is the whole point of
// the fixture -- with it dropped silently, doctor sees an entity with a
// repository and no policy, and says so.
type InvoicePolicy struct{

func (InvoicePolicy) Can() error { return nil }
