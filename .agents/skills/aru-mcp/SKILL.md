---
name: aru-mcp
description: Change what `aru mcp` serves to a developer's assistant -- add, rename or change a tool (doctor, project_map, where_does_it_go, feature_recipe, commands, imports_catalog, generate), change the implementation contract it answers from (internal/contract/contract.json), or touch what the generate tool may run or write. Use when the request is "add a tool to aru mcp", "where_does_it_go answers the wrong path", "add a recipe", "a card is missing", "let the assistant run X", or when an MCP client reports a line it cannot parse. Covers the stdout rule, the one write tool and what it never does, and why the contract is one embedded file.
license: MIT
metadata:
  audience: contributor
---

# Changing aru mcp

`aru mcp` is `mcp.go` at the root, on the protocol of `github.com/arandu-io/mcp`
(`mcp.Local`). It is the second server beside `aru lsp`: the same analyses,
for an assistant rather than an editor.

```sh
grep -c 'Tool{root\|Tool{}' mcp.go          # the tools developerServer lists
grep -c '"kind":' internal/contract/contract.json   # contract cards
grep -c '"name":' internal/contract/contract.json   # recipes
```

## Three rules that are not negotiable

1. **Standard output carries protocol frames and nothing else.** A log line
   there is read by the client as a message it cannot parse, and the server
   looks broken while working. `serveMCP` logs to the stream it is handed for
   logs; a tool never prints. A tool that runs a command hands it buffers.
   `TestTheDeveloperServerAnswersEveryToolOverStdio` parses every stdout line
   as JSON-RPC and fails on the first that is not.
2. **One tool writes, and only on `apply=true`.** `generate` runs a `make:*`
   with `--dry-run` first, every time, checks the plan, and writes only when
   asked. Every generator takes `--dry-run`
   (`TestEveryGeneratorPreviewsWithoutWriting`), which is what makes the
   preview uniform.
3. **Nothing here runs a migration or edits wiring.** `generate` refuses a
   command that is not `make:*` and a plan naming a path under `bootstrap/` or
   `routes/` (`untouchable`). The wiring a generator prints is returned for a
   person to paste, as in a terminal.

## The contract is one file with two readers

`internal/contract/contract.json` is embedded. `where_does_it_go` and
`feature_recipe` answer from it, and the doctor attaches the card its rule
verifies to each finding (`Finding.Contract`). Change a card there and both
move; never copy a card's text into either. A recipe step that names a
generator is one a person runs, and the code it leads to has to compile. No
service dispatches a job: `app/Jobs` imports `app/Services` as soon as a
handler takes a service, so a service importing `app/Jobs` back is an import
cycle. The service stores an event in the outbox inside its transaction, and
a listener built with the queue dispatches the job after the commit (the
job recipe and item 6 of the service card; in the webhook recipe the listener
does the work itself), and work on a clock is dispatched by a task in the
provider's `Schedule()`. A card's `example` is a path of the skeleton release
`aru new` pins, read from the module cache.

Tests that hold it:

- `tests/Unit/contract`: every rule a card names is a doctor rule; the six
  recipes the decision names exist; no card teaches a bridge import; the
  webhook recipe stores an event for a listener and dispatches no job; the
  job recipe dispatches from a listener or a scheduled task and the service
  card forbids importing `app/Jobs`; the event, listener and job cards name
  the skeleton's examples, and every example a card names exists in the
  pinned skeleton (`TestEveryExampleIsAFileOfThePinnedSkeleton`).
- `TestTheGeneratedModuleCompiles` (`tests/Unit/gen`) builds the job recipe
  written out: the listener that dispatches `SettlePurchaseOrder`, whose
  handler takes services, after the approval service stored its event.
- `TestEveryGeneratorACardNamesIsACommand`: every generator a card or a
  recipe names is a command of this binary.

The cards follow `00-meta/DOC-implementation-contract.md` in the project
documents and the canonical imports of `aru imports:catalog`.

## Adding a tool

1. A type with `Name`, `Description`, `Schema` and `Handle`, in `mcp.go`.
   The description is what the model reads to decide to call it: say what it
   returns and when to ask.
2. Answer with `jsonText` for data, so every tool answers one format.
3. Add it to `developerServer` and to the call list of
   `TestTheDeveloperServerAnswersEveryToolOverStdio`, with a string the answer
   must carry.
4. A tool that needs the command table reads it through `mcpCommands`, which
   `init` assigns: the table refers to `runMCP`, and Go refuses the cycle.
