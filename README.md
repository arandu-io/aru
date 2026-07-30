# aru

The Arandu CLI. Same relationship as Laravel and `artisan`: the brand is
**Arandu**, the command is **`aru`** — the `arandu` binary is already taken by
another tool.

Its own module, with no dependencies. It is separate from
`arandu-io/framework` so that a project importing the framework does not drag
the CLI's dependencies along; see `05-repositorios.md` and `adr/0005`.

## Two kinds of command

| Kind | Commands | Why |
|---|---|---|
| Runs here | `key:generate`, `new`, `make:*`, `doctor` | touches files only |
| Delegated to the project | `serve`, `migrate`, `routes` | needs the registered modules, and only the project binary knows them |

Delegation runs `go run ./cmd/app <command>` from the project root. That is a
consequence of explicit wiring: there is no container and no plugin loading, so
a separately compiled CLI cannot know which modules an application registered.

## Status

Phase 1 implements `key:generate`, `serve`, `migrate` and `routes`. `new`,
`make:module`, `make:policy` and `doctor` state the phase they arrive in.

## Build

```
go build -ldflags "-X main.version=$(git describe --tags --always)" -o bin/aru .
```

## License

MIT, the same license Laravel uses. See `LICENSE.md`.
