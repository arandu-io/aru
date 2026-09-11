module github.com/arandu-io/aru

go 1.26.0

retract v0.37.0 // Does not pin the skeleton and could clone an incompatible moving project baseline.

// The CLI lives in its own module on purpose. If it lived inside the framework,
// every project that imports the framework would drag the CLI's dependencies
// along -- see 00-meta/DOC-repositories.md and 10-adr/ADR-0006-cli-in-separate-module.md.
//
// Three dependencies. The first is the DSL's: YAML has no parser in the standard
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
// esbuild compiles browser assets through its Go API. It is linked only into
// this CLI, never into an application, and needs no Node runtime or npm.
//
// The five that follow arrived together, with the packager: producing an APK,
// an IPA or a signed executable means resizing icons, running toolchain steps
// in parallel, reading a package graph, writing a Windows resource section and
// encoding a manifest. None of them is linked into an application, and none is
// reachable from a project -- they are this CLI's, on the same terms as
// esbuild.
//
// The core has one direct require, golang.org/x/crypto, plus the x/sys that
// comes with it. That separation is the whole point of ADR 0006: the CLI can
// afford a dependency, and every project that imports the framework must not
// pay for it.

require (
	github.com/akavel/rsrc v0.10.1
	github.com/arandu-io/hesape v0.36.0
	github.com/evanw/esbuild v0.28.2
	golang.org/x/image v0.43.0
	golang.org/x/sync v0.23.0
	golang.org/x/text v0.42.0
	golang.org/x/tools v0.50.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
