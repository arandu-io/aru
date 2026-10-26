module github.com/arandu-io/aru

go 1.26.10

retract (
	v0.81.0
	v0.37.0 // Does not pin the skeleton and could clone an incompatible moving project baseline.
)

// The CLI lives in its own module on purpose. If it lived inside the framework,
// every project that imports the framework would drag the CLI's dependencies
// along -- see 00-meta/DOC-repositories.md and 10-adr/ADR-0006-cli-in-separate-module.md.
//
// The direct requirements below are the whole budget, and the allow-list in
// .github/workflows/ci.yml holds the modules they link to it.
//
// yaml.v3 is the DSL's: YAML has no parser in the standard library, and writing
// one would be a subset that a model eventually writes outside of. It has no
// dependencies of its own.
//
// hesape is a component the generator calls rather than a library it borrows.
// The merge that carries a custom block across a regeneration lived here and
// lived again elsewhere, and two implementations of it answer "was the file I
// edited overwritten?" differently -- which is the one question the escape
// hatch exists to answer the same way every time.
//
// framework is linked for its test kit and nothing else, framework/test and
// the packages under it: the trace a failing journey writes, the native Chrome
// driver and the page a journey drives. e2e:explain reads the trace with the
// kit's reader, e2e:record and the browser tools of aru mcp drive Chrome with
// the kit's driver, and none of them keeps a second copy of either. The kit is
// the standard library underneath, and hesape, so nothing else enters.
//
// mcp is the protocol `aru mcp` speaks to a developer's assistant over stdio.
// Its library package imports hesape and the standard library and nothing
// else: the framework its module requires is reached only by its own tests.
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
	github.com/akavel/rsrc v0.10.2
	github.com/arandu-io/framework v0.67.0
	github.com/arandu-io/hesape v0.88.0
	github.com/arandu-io/mcp v0.5.0
	github.com/evanw/esbuild v0.28.2
	golang.org/x/image v0.47.0
	golang.org/x/sync v0.24.0
	golang.org/x/text v0.43.0
	golang.org/x/tools v0.51.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)
