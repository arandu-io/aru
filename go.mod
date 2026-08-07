module github.com/arandu-io/aru

go 1.25

// The CLI lives in its own module on purpose. If it lived inside the framework,
// every project that imports the framework would drag the CLI's dependencies
// along -- see docs/05-repositorios.md and docs/adr/0006.
//
// One dependency, and it is the DSL's: YAML has no parser in the standard
// library, and writing one would be a subset that a model eventually writes
// outside of. yaml.v3 has no dependencies of its own, which keeps the graph at
// exactly one node.
//
// The core has one direct require, golang.org/x/crypto, plus the x/sys that
// comes with it. That separation is the whole point of ADR 0006: the CLI can
// afford a dependency, and every project that imports the framework must not
// pay for it.

require gopkg.in/yaml.v3 v3.0.1
