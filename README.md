# devnotes

A meta-language for developer notes, with a tree-sitter grammar, a Go
validation engine, and a Neovim integration.

This is a monorepo. The repo root is simultaneously:

- a **tree-sitter grammar** (`grammar.js`, `src/`, `queries/`)
- a **Neovim plugin** (`lua/`, `plugin/`, `ftdetect/`)
- a **Go workspace** (`go.work` → `engine/`, `bindings/go/`)

## Install

### Neovim plugin + parser

The repo root is the plugin. With [lazy.nvim](https://github.com/folke/lazy.nvim):

```lua
{
  'asaidimu/devnotes',
  lazy = false,
  build = ':TSInstall devnotes', -- compiles src/parser.c from the repo
}
```

- `lazy = false` runs `setup()` on startup, which registers the local
  `devnotes` parser with nvim-treesitter via the `User TSUpdate` hook and
  adds `devnotes` filetype detection for `*.dn` / `*.devnotes`.
- The `build` step runs `:TSInstall devnotes`, which compiles the parser
  from this repo's `src/parser.c` and links `queries/`. (You can instead
  run `:TSInstall devnotes` manually after install.)
- In Go / TypeScript / JavaScript files, press `F3` (or run `:DevnotesF3`)
  to highlight DevNotes found in comments.

Requirements: nvim 0.12+, nvim-treesitter, a C compiler (for the parser).

### Go engine (CLI)

```sh
go install github.com/asaidimu/devnotes/engine/cmd/devnotes@latest
devnotes check <file-or-dir>
```

### LSP server

```sh
go install github.com/asaidimu/devnotes/engine/cmd/devnotes-lsp@latest
```

Wire it into nvim (nvim 0.11+ lspconfig):

```lua
vim.lsp.config('devnotes', {
  cmd = { 'devnotes-lsp' },
  filetypes = { 'devnotes' },
})
vim.lsp.enable('devnotes')
```

`devnotes-lsp` speaks JSON-RPC on stdio (`initialize`, `textDocument/didOpen`,
`textDocument/didChange`) and publishes diagnostics via `publishDiagnostics`.

## Layout

```
grammar.js / src/ / queries/   tree-sitter grammar + highlight queries
lua/ plugin/ ftdetect/         Neovim integration (F3 in-comment highlighting)
bindings/go/                   Go binding module (subdirectory module of this repo)
engine/                        Go engine: normalize, model, validate, pipeline,
                               cst, host adapters, CLI, LSP server
go.work                        local Go workspace (engine + bindings/go)
```

### Local Go development

The workspace ties `engine/` and `bindings/go/` together locally:

```sh
go build ./engine/...
cd engine && go test ./...
```

`engine/go.mod` carries a `replace` pointing at `../bindings/go` for local
builds; downstream consumers ignore it and resolve the tagged subdirectory
module `github.com/asaidimu/devnotes/bindings/go`.

## Tests

```sh
npm test                 # grammar corpus + basic node tests
cd engine && go test ./...
```

## Spec

See [SPEC.md](SPEC.md) for the DevNotes language and validation rules.
