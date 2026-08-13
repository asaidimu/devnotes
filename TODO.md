# DevNotes integration + Level 2 toolchain

Markers: `[ ]` todo, `[-]` in progress, `[*]` done

## A. Publish
- [ ] Initial git commit
- [ ] Add GitHub remote and push

## B. nvim parser baseline (pure grammar)
- [ ] Verify query layout expected by the new neovim-treesitter registry
- [ ] `local_parsers` entry for devnotes + `:TSInstall devnotes`
- [ ] `vim.filetype.add` for .dn/.devnotes
- [ ] Verify `.dn` highlighting + `:checkhealth treesitter`

## C. Go engine
- [ ] Enable Go bindings (`tree-sitter.json` bindings.go=true), regenerate, confirm `bindings/go` builds
- [ ] `engine/` Go module: go.mod + deps (go-tree-sitter runtime, tree-sitter-typescript)
- [ ] `internal/cst` — devnotes CST parse + iteration
- [ ] `internal/model` — SPEC 30 DevNote from CST
- [ ] `internal/normalize` — SPEC 5 comment normalization (line/block markers, decorative prefix, indentation)
- [ ] `internal/validate` — SPEC 31 diagnostics (MISSING_TITLE, duplicate core fields, invalid priority/timestamp, duplicate IDs, unresolvable refs)
- [ ] `internal/host/go` — comment extraction via go/token
- [ ] `internal/host/ts` — comment extraction via tree-sitter-typescript for ts/tsx/js/jsx
- [ ] `cmd/devnotes` — `devnotes check` CLI (text/JSON, exit codes)
- [ ] `cmd/devnotes-lsp` — LSP server with publishDiagnostics
- [ ] Go tests + golden tests; fixtures `tests/fixtures/{sample.go,sample.ts}` seeded with issues
- [ ] Extend `test.yaml` CI with `go test ./...`

## D. nvim Lua adapter (F3)
- [ ] `lua/devnotes/init.lua` — comment node iteration + normalization
- [ ] Highest-priority piece: CST -> buffer-range mapping (prefix lengths, go grouped comments)
- [ ] Apply highlights via `vim.hl.range`
- [ ] `plugin/devnotes.lua` — autocommands + toggle command
- [ ] Manual verification in nvim (:Inspect on `// @note`, multi-line, `/* */`, go grouped, esp trailing comments)

## E. nvim LSP wiring
- [ ] `vim.lsp.config` for devnotes-lsp (cmd -> built binary)
- [ ] Autostart on *.go, *.ts, *.tsx, *.js, *.jsx
- [ ] Verify diagnostics in a go and a ts buffer

## Verification
- [*] Corpus 35/35 + npm test green (no grammar changes)
- [ ] `go test ./...` + `devnotes check` on fixtures
- [ ] Push -> `:TSInstall devnotes`, `.dn` highlighting
- [ ] Lua adapter + LSP diagnostics live in nvim