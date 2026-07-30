module github.com/arandu-io/aru

go 1.25

// The CLI lives in its own module on purpose. If it lived inside the framework,
// every project that imports the framework would drag the CLI's dependencies
// along -- see docs/05-repositorios.md and docs/adr/0005.
//
// It has no dependencies of its own: the interactive prompt library (bubbletea)
// arrives in phase 2, with the code generator that needs it.
