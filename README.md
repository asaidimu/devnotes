# devnotes

A meta-language for developer notes, with a tree-sitter grammar, a Go
validation engine, and a Neovim integration.

This is a monorepo. The repo root is simultaneously:

- a **tree-sitter grammar** (`grammar.js`, `src/`, `queries/`)
- a **Neovim plugin** (`lua/`, `plugin/`, `ftdetect/`)
- a **Go module** (`go.mod`; packages under `engine/` and `bindings/go/`)

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
  filetypes = { 'devnotes', 'go', 'typescript', 'typescriptreact', 'javascript', 'javascriptreact' },
  root_markers = {},
})
vim.lsp.enable('devnotes')
```

`devnotes-lsp` speaks JSON-RPC on stdio (`initialize`, `textDocument/didOpen`,
`textDocument/didChange`) and publishes diagnostics via `publishDiagnostics`.
It also serves context-aware completions through `textDocument/completion`:

- inside an `@note` header: categories, statuses, priorities, directives, and
  existing note IDs (right after `#`) for reuse;
- after `@see #` or a bare `#`: every note ID defined in the workspace (a
  per-file scan indexes IDs on open/change/save), annotated with the file and
  line where the note lives — no need to keep IDs in your head;
- after the title colon (`:`): nothing (the title is free text).

IDs are a tooling concern (unique + stable), titles are for humans — so the
`note` snippets auto-generate a short random UID for the `#id` slot.

### Snippets

The plugin registers LuaSnip snippets for the `devnotes` filetype when LuaSnip
is available (disable with `require('devnotes').setup({ load_snippets = false })`):

- `note` — `@note #<uid> observation : title` (UID auto-generated, editable)
- `note-observation` / `note-todo` / `note-issue` / `note-context` / `note-lesson` / `note-prompt` — same header with the category preset
- `author` — `@author name`
- `see` — `@see #id`
- `note-full` — complete note with directives and body

## Layout

```
grammar.js / src/ / queries/   tree-sitter grammar + highlight queries
lua/ plugin/ ftdetect/         Neovim integration (F3 in-comment highlighting)
snippets/                      LuaSnip snippets for the devnotes filetype
bindings/go/                   Go binding for the devnotes grammar
engine/                        Go engine: normalize, model, validate, pipeline,
                               cst, host adapters, CLI, LSP server
go.mod                         single Go module (all engine + binding packages)
```

### Local Go development

Everything lives in one module at the repo root:

```sh
go build ./...
go test ./engine/...
```

`bindings/go` is a plain package of this module, so no workspace or `replace`
directive is needed; consumers resolve the whole module via the `v0.1.0` tag.

## Tests

```sh
npm test                 # grammar corpus + basic node tests
go test ./engine/...
```

## Spec

See [SPEC.md](SPEC.md) for the DevNotes language and validation rules.
