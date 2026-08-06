package billing

// This file does not parse: the brace is never closed. It is the whole point of
// the fixture -- with it dropped silently, doctor sees a module with a
// repository and no policy, and says so.
type Policy struct{

func (Policy) Can() error { return nil }
