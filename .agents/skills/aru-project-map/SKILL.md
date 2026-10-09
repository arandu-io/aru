---
name: aru-project-map
description: Change what `aru lsp` answers about a project beyond the view language -- the arandu/projectGraph map in either schema, its node kinds, edge kinds and groups, the navigation built on it (definition on a route string, references along the edges, document and workspace symbols), the doctor findings published as diagnostics, and arandu/catalog. Use when the request is to "add a kind to the project map", "the map shows X as application", "add an edge", "the editor cannot jump from a route", "publish doctor findings in the editor", "the extension needs a new field", or when vscode-arandu needs something the server does not answer yet. Covers why schema 1 is frozen byte for byte, where a kind is read from, why the edge kinds are a closed set that states its reach, and the two things the language server never does.
license: MIT
---

# Changing the project map

The map is built by `internal/doctor` from the same parse as the findings, and
served by `internal/lsp`. Two schemas live side by side:

| schema | built by | served when |
| --- | --- | --- |
| 1 | `buildProjectGraph`, `internal/doctor/project_graph.go` | `arandu/projectGraph` with no params, null, `{}` or `{"schemaVersion":1}` |
| 2 | `buildProjectMap`, `internal/doctor/project_map*.go` | `arandu/projectGraph` with `{"schemaVersion":2}` |

```sh
awk '/^var mapEdgeKinds = /,/^}/' internal/doctor/project_map.go | grep -c 'Kind: '   # 9  edge kinds
awk '/^var mapGroups = /,/^}/' internal/doctor/project_map.go | grep -c 'ID:'        # 12 groups
```

## Schema 1 does not move

`tests/Unit/lsp/schema1_golden_test.go` compares the result with goldens
written by the server before schema 2 existed, byte for byte, on three
fixtures. Those bytes are what an editor adapter built against schema 1
parses, and it refuses a result that gains a group, renames a kind or reorders
anything.

So a change that makes that test fail is a change to put in schema 2 instead.
The `-update-schema-one-golden` flag exists to write the goldens once, and
running it to make a change pass is moving the contract under every installed
adapter. A change schema 1 genuinely needs is a schema 3 and a decision record.

The schema 1 branch of `arandu/projectGraph` calls `doctor.Analyze` on the
conventional profile, fresh, as it always did. Schema 2 and the navigation
read `project.analysis()`, cached by a stamp of the tree, on the profile
`doctor.DeclaredProfile` reads from `arandu.mod.toml`. Do not route schema 1
through the cache: the profile would follow the manifest and the bytes with it.

## A kind is read from what the file declares

`anatomyOf` decides, in this order, and the folder is only the fallback:

1. the interface the file asserts with `var _ I = T{}` -- `contractKinds`, keyed
   by import path and name, with the framework bridge and the hesape path both
   listed;
2. an import only one kind takes (the factories package);
3. the shape of what it declares: an entity embedding the model core, an
   `init` that registers a migration, a method with the action signature, a
   `Validate() validation.Errors`, an `Event(string) events.Event`, a function
   answering a `Middleware`;
4. `graphArtifactForFile`, the folder reading schema 1 uses.

A generated file -- `ast.IsGenerated`, or the header `model:build` writes --
is marked `generated` and joins a feature without opening one. Only the kinds
in `featureOpeners` open a feature.

To add a kind: plant it in `tests/mapproject.go`, copied from its generator's
golden under `internal/gen/testdata/stubs` when there is one, add the row to
`TestTheMapClassifiesByWhatAFileDeclares`, give it a group in `mapGroupOf`,
then write the reading. A kind no group lists is a node the editor never shows.

## The edge kinds are a closed set that states its reach

`mapEdgeKinds` is the list the decision record names -- routes-to,
validates-with, authorizes, persists, renders, tested-by, dispatches,
listens-to -- plus contains. Each entry says what the analysis follows to draw
the edge and what it does not, and the map carries that text to the client,
because an edge the analysis cannot see has to be told apart from an edge that
is not there. Widening what an existing kind follows means changing its
`Follows` and `DoesNotFollow` in the same commit; adding a kind is a decision,
not a patch.

Every edge read from the code carries `At`, the place it was read from;
containment carries none. The references request is built on `At`, so an edge
without it is an edge the editor cannot walk.

Each kind has its near misses in `TestTheEdgesAreReadFromTheCode`: a
controller formatting a model does not persist it, a policy naming its model
does not either. A new reading arrives with the near miss it must stay quiet
on.

## What the language server never does

It runs nothing. No generator, no migration, no build, no `go list`: every
answer comes off the disk of the tree it was initialized on, and an editor
asks on every keystroke.

It does not answer where the Go language server answers. Definition in Go
source answers only on a string literal -- a view name, a route name passed to
`URL`, `RedirectRoute`, `Route` or `Name`, a string of a registration in
`routes/` -- because gopls answers every identifier, and two destinations for
one click make the person decide which server was right. References answer
the `At` of the edges touching the declaration, outside its own file.

The doctor's findings are published as diagnostics only to a client that sends
`{"doctorDiagnostics": true}` in its initializationOptions. An adapter that
draws them from the map's diagnostic nodes would otherwise show every finding
twice.

## The checks

```sh
GOWORK=off go test -count=1 ./tests/Unit/lsp ./tests/Unit/doctor .
```

Then the gates in `AGENTS.md`. A change to what the map answers is also a
change for `vscode-arandu`, whose parser checks the shape; say what moved in
the handoff, with a sample node and edge of each kind that changed.
