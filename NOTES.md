# devnotes CLI extension — build notes

## What's new

- `engine/internal/index` — workspace-wide JSON index (`.devnotes/index.json`).
  `Build`, `Load`, `Save`, `Update` (incremental, hash-diffed), `Status`
  (stale/missing/untracked files).
- `engine/internal/noteedit` — writes note mutations directly into the host
  source comment (source is the record of truth; the index is a disposable
  cache re-synced after every edit).
- `engine/cmd/devnotes` — rebuilt on Cobra:
  - `check [paths...]` — same diagnostics as before, ported as-is.
  - `index init|update|status`
  - `note add|claim|resolve|status|priority`
  - `list`, `show <id>`, `trace <id>`, `report`
  - Global flags: `--index`, `--root`, `--json`.

Run `devnotes --help` and `devnotes <command> --help` for full usage; every
subcommand's flags are documented there.

## Building

```
go mod tidy   # first time only, resolves gopkg.in/yaml.v3 etc. normally
go build ./engine/cmd/devnotes
```

## One dependency note

`go.mod` pins `github.com/tree-sitter/go-tree-sitter` to a specific commit
(`v0.24.1-0.20251112183152-c9492002f76e`) rather than the `v0.25.0` tag the
original repo used. That tag was deleted upstream at the time of this work
(the repo now only has tags up to `v0.24.0`), and `v0.24.0` doesn't support
this project's grammar ABI version (14 vs. the 15 the grammar needs — you'll
see `SetLanguage` silently fail and zero notes get parsed). The pinned
commit is the current upstream `master`, which does support ABI 15 and is
what this whole implementation was built and tested against. If `v0.25.0`
still resolves for you (Go's module proxy is supposed to keep published
versions available even after a tag is deleted upstream), feel free to
switch back to it — just confirm `go test ./engine/...` still passes first.

## Verified end-to-end (see PROPOSAL.md for command-by-command output)

`check`, `index init/update/status`, `note add/claim/resolve/status/priority`,
`list`, `show`, `trace --direction=out|in`, `report --by=...` were all run
against a real scratch file and inspected line-by-line, including two edge
cases the engine's own doc comments were misleading about (block-end range
semantics, and directive-vs-body-text ordering per the grammar) — both are
called out with the reasoning in the source comments where they matter
(`noteedit.go`'s `AppendBody`/`blockLastLine`).

## Not yet done

- No unit tests for `index`/`noteedit` yet (both were validated via live
  end-to-end runs against the real binary, not `go test`). Worth adding
  table-driven tests before this goes into CI.
- `note add`'s default (no `--line`) appends at literal end-of-file with no
  blank-line separation from preceding code — functionally fine, slightly
  ugly output. Could auto-insert a blank line first.
- `trace` only follows references shaped `@see #id`; bare-URL `@see`
  targets are intentionally skipped for graph-walking (nothing to walk to).
