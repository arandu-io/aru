# Skills

Procedures an assistant follows when changing this CLI.

They live in `.agents/skills/<name>/SKILL.md`, which is the path the coding
assistants read from — Cursor, Codex, Cline, Copilot, Gemini CLI, Amp, OpenCode,
Warp, Zed and the rest all look there. It is one directory rather than a file
per vendor, so a skill written once is read by whatever this project is being
written with.

Each file opens with frontmatter carrying a `name` and a `description`. The
description is what a tool reads to decide whether the skill is relevant, so it
names the situation you are in rather than the topic it covers.

| skill | when it fires |
| --- | --- |
| `aru-command` | adding, renaming or changing a command, a flag or what `aru help` prints |
| `aru-doctor-rule` | adding, widening or removing a rule of `aru doctor` |
| `aru-view-compiler` | changing `internal/kyse`: the parser, the generator, an escape, a directive |
| `aru-generator` | changing what `make:*`, `generate` or `new` writes into somebody's project |

## Why these exist

Everything in this repository writes something that somebody else then has to
live with, and that is what makes the four ways of getting it wrong specific.

A command is a promise typed by hand: 39 of them are in the help output, ten
forward to a binary this one does not control, and a name is not retractable
once a project's scripts contain it.

A doctor rule is a security check whose only failure mode is silence. It fires
on nothing, nobody notices, and the finding it was written for ships. The suite
holds a test that walks the rule set and demands every rule produce a finding on
a planted fixture, which is why a rule arrives with its fixture or does not
arrive.

The view compiler writes Go into a file whose header says not to edit it. When
that Go is wrong the person reads a compiler error against code they never wrote
— and for four rounds the wrongness was a name: a loop binding took the
generator's temporary, then its package, then the package a view was calling for
itself, and each time the build printed how many views it had compiled and
exited 0.

The generators emit an entity, a policy, a repository and four screens that a
project keeps. A change to a template changes what everyone regenerates, so the
output is pinned by golden files and the specification is pinned by a schema
generated from the validator's own constants.

## Adding your own

A skill in this directory travels with the repository. Keep it a procedure
rather than a description: a file that says "read the documentation" never
changes what anybody does. Every command in one has to be a command that runs,
and every number in one has to be a number somebody measured.
