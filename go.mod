module github.com/arandu-io/aru

go 1.26

retract v0.37.0 // Does not pin the skeleton and could clone an incompatible moving project baseline.

// The CLI lives in its own module on purpose. If it lived inside the framework,
// every project that imports the framework would drag the CLI's dependencies
// along -- see 00-meta/DOC-repositories.md and 10-adr/ADR-0006-cli-in-separate-module.md.
//
// Two dependencies. The first is the DSL's: YAML has no parser in the standard
// library, and writing one would be a subset that a model eventually writes
// outside of. yaml.v3 has no dependencies of its own, which keeps that half of
// the graph at exactly one node.
//
// The second is hesape, and it is a component the generator calls rather than a
// library it borrows. The merge that carries a custom block across a
// regeneration lived here and lived again elsewhere, and two implementations of
// it answer "was the file I edited overwritten?" differently -- which is the one
// question the escape hatch exists to answer the same way every time.
//
// The core has one direct require, golang.org/x/crypto, plus the x/sys that
// comes with it. That separation is the whole point of ADR 0006: the CLI can
// afford a dependency, and every project that imports the framework must not
// pay for it.

require (
	github.com/arandu-io/hesape v0.25.0
	gopkg.in/yaml.v3 v3.0.1
)
