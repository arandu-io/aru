package Ledger

// Entry is a line of the ledger.
//
// The package clause is capitalised, so every import of it reads as an exported
// name that is not one. go vet has nothing to say about this.
type Entry struct {
	Amount int64
}
