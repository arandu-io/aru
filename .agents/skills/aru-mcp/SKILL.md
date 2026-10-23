---
name: aru-mcp
description: Change what `aru mcp` serves to a developer's assistant -- add, rename or change a tool (doctor, project_map, where_does_it_go, feature_recipe, commands, imports_catalog, generate, and the e2e_ browser tools), change the implementation contract it answers from (internal/contract/contract.json), or touch what the generate tool may run or write. Use when the request is "add a tool to aru mcp", "where_does_it_go answers the wrong path", "add a recipe", "a card is missing", "let the assistant run X", or when an MCP client reports a line it cannot parse. Covers the stdout rule, the one write tool and what it never does, and why the contract is one embedded file.
license: MIT
metadata:
  audience: contributor
---

# Changing aru mcp

`aru mcp` is `mcp.go` at the root, on the protocol of `github.com/arandu-io/mcp`
(`mcp.Local`). It is the second server beside `aru lsp`: the same analyses,
for an assistant rather than an editor.

```sh
grep -c 'Tool{root\|Tool{}' mcp.go          # the reading tools and generate
grep -c 'name: "e2e_' mcpbrowser.go          # the browser tools
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
  card forbids importing `app/Jobs`; the job card's `calledBy` names the
  worker, a listener and a scheduled task, and no clause of any card or
  recipe has a service dispatch a job
  (`TestNoCardOrRecipeHasAServiceDispatchAJob`); the event, listener and
  job cards name the skeleton's examples, and every example a card names
  exists in the pinned skeleton (`TestEveryExampleIsAFileOfThePinnedSkeleton`).
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

## The browser tools

`mcpbrowser.go` holds eight tools -- `e2e_open`, `e2e_accessibility_tree`,
`e2e_click`, `e2e_fill`, `e2e_expect`, `e2e_capture`, `e2e_read_trace`,
`e2e_close` -- over one `e2eSession`: the test kit's `browser.Page` on the
native `cdp` driver, opened by the first `e2e_open` and closed by `e2e_close`
or when the serve ends. They are the kit's page and not a second driver
written here, so the assistant meets the same waiting, the same locators and
the same automatic failures (an exception, a console error, a 5xx) as a
journey in `tests/E2E`.

- The page reports to `mcpT`, which stands in for a test: `Fatalf` unwinds to
  the tool with `stepFailed`, the tool answers it with `IsError`, and the page
  stays open. `mcpT` embeds `testing.TB` only to satisfy the interface; a
  method the page starts calling that it does not override panics, and the
  stdio test is what finds it.
- `e2e_read_trace` writes the session's trace where the page writes it on
  close (`trace.Dir("aru-mcp")`) and answers `explainTrace`, the same
  explanation `aru e2e:explain` prints.
- `e2e_accessibility_tree` and `aru e2e:record` name elements with one piece
  of JavaScript, `elementNames` in `e2erecordscript.go`.
- Their answers say that a defect found this way ends as a journey (`aru
  make:e2e`, `aru e2e:record`): exploration is not where a regression is kept.

`TestTheBrowserToolsDriveThePage` (`mcpbrowser_internal_test.go`) drives all
of them over stdio against a small site with a headless Chrome; it skips with
no browser and fails in CI. A new browser tool goes in `e2eTools` and in that
test, not in `TestTheDeveloperServerAnswersEveryToolOverStdio`, which needs no
browser and only lists them.
