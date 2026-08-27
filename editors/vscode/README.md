# Syntax highlighting for `.kyse.go`

A `.kyse.go` is not Go. Without telling the editor so, gopls parses it, marks
every directive as a syntax error, and the file is red while being correct.

## What is here

| File | What it is |
|---|---|
| `settings.json` | Copied into the project's `.vscode/` by `aru new`. It carries the part that matters: it associates `.kyse.go` with the `kyse` language, turns format-on-save off for it, and hides the generated views from search |
| `kyse.tmLanguage.json` | The TextMate grammar: directives, `{{ }}`, `{!! !!}` highlighted apart from it because that is where XSS gets in, an unknown `@directive` marked invalid, and the `@go` block highlighted as Go |
| `language-configuration.json` | Block comments, bracket pairs, and indentation: `@section`, `@if`, `@foreach`, `@for`, `@while` and `@go` indent; their `@end` counterparts, `@else` and `@elseif` outdent |

Only `settings.json` reaches a project. `aru new` carries it inside the binary
and writes it to `.vscode/settings.json`, so it arrives on a machine that has
nothing installed but `aru`. Nothing installs the other two files.

## What is not here, and why

There is no `package.json`, which is the manifest a VS Code extension needs.
Without it these files are not an installable extension.

That is a decision rather than an oversight. No Arandu project needs Node to
build or to run, and no repository of this project carries a `package.json`; the
verification script sweeps every one of them and fails if a manifest, a lockfile
or a `node_modules` appears, in this directory as readily as anywhere else.

Editor tooling is not a build dependency, and there is an argument to be made
from that. But the constraint is written without an exception, and opening one
quietly is how Node has arrived in comparable projects: through a single case
that looked harmless on its own.

**So it stays open, for the owner to decide.** Three ways out:

1. **Its own repository**, `arandu-io/vscode-kyse`. Creating one is the owner's
   call. It isolates the problem completely: the `package.json` never touches
   `aru`
2. **A written exception** for `aru/editors/`, recorded together with the
   reasoning that editor tooling is not a build dependency
3. **Leave it as it stands.** `settings.json` already delivers the essential
   part — the editor stops reporting errors that are not there. The fine-grained
   highlighting goes unpublished

## Installing by hand in the meantime

```bash
mkdir -p ~/.vscode/extensions/kyse
cp kyse.tmLanguage.json language-configuration.json ~/.vscode/extensions/kyse/
# and write the package.json locally, outside every repository of this project
```
