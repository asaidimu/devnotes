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
It implements the DevNotes LSP Feature Specification v1.0 (LSP-3 conformance):

- **Diagnostics** — plain-language messages (`Note #x has no title. Type the
  title after the ':' …`) with machine-readable `code`s, cross-file
  `relatedInformation` for duplicate IDs, and structured `data` for quick
  fixes. Unparseable notes surface as `INVALID_HEADER` instead of vanishing.
  Pull diagnostics (`textDocument/diagnostic`, `workspace/diagnostic`) are
  served when the client supports them.
- **Hover** — full note preview (metadata, tags, authors, reference count,
  body excerpt, stale-status banner) and target previews on `@see` values.
- **Listing** — `workspace/symbol` plus the `devnotes.list` command with
  structured filters (category/status/priority/tag/author/file/text/reference),
  sorting and pagination; `devnotes.get`, `devnotes.nextInFile`,
  `devnotes.prevInFile`, `devnotes.graph`, `devnotes.exportContext` and
  `devnotes.reindex` round out the command surface.
- **Completion** — context-aware inside headers and directives: note IDs
  (proximity-ranked, `filterText` without `#`, preview via
  `completionItem/resolve`), tags with usage counts, categories (core +
  custom), statuses, priorities, an "today" timestamp and template snippets.
- **Code actions** — quick fixes for every fixable diagnostic, status and
  priority transitions with canonical insertion, `Assign to…` from config,
  `@see` add/remove, and note/file canonical formatting.
- **Navigation & edits** — `definition`/`references` across the workspace,
  `documentLink` on every `@see`, `documentSymbol`, workspace-wide `rename`
  with `prepareRename` (rejects ambiguous IDs), canonical `formatting`
  (safe inside Go/TS comments), `foldingRange`, `selectionRange`,
  `inlayHint` and `codeLens`.
- **Configuration** — `workspace/didChangeConfiguration` or `devnotes.toml`:

  ```toml
  [devnotes]
  defaultAuthor = "jane"
  knownAuthors = ["jane", "bob"]
  categories = ["architecture"]        # additive custom categories
  [devnotes.hover]
  bodyExcerptChars = 2000
  [devnotes.list]
  defaultLimit = 200
  ```

Wire it into nvim (nvim 0.11+ lspconfig):

```lua
vim.lsp.config('devnotes', {
  cmd = { 'devnotes-lsp' },
  filetypes = { 'devnotes', 'go', 'typescript', 'typescriptreact', 'javascript', 'javascriptreact' },
  root_markers = { 'devnotes.toml', '.git', 'package.json' },
})
vim.lsp.enable('devnotes')
```

The bundled plugin does this automatically (`require('devnotes').setup()` or
just installing it) and adds the reviewer surface:

- `<C-p>` — floating preview of the note or reference under the cursor
- `<leader>dca` — DevNotes-only code actions (status/priority/assign/format)
- `:DevNotesList`, `:DevNotesListIssues`, `:DevNotesListTodos` — pickers over
  `devnotes.list` (vim.ui.select, or `:Telescope devnotes list` with the
  Telescope extension)
- `:DevNotesNext` / `:DevNotesPrev` — cycle notes in the file
- `:DevNotesFormat` — explicit canonical formatting (never on save)
- `require('devnotes.statusline').component()` — current note's
  `category · status · priority` for your statusline

IDs are a tooling concern (unique + stable), titles are for humans — so the
`note` snippets auto-generate a short random UID for the `#id` slot.

### Snippets

The plugin registers LuaSnip snippets when LuaSnip is available (disable with
`require('devnotes').setup({ load_snippets = false })`). The same snippets load
for the `devnotes` filetype **and** for the host filetypes — `go`,
`typescript`, `typescriptreact`, `javascript`, `javascriptreact` — so you can
write notes directly inside Go/TS/JS comments without ever opening a `.dn`
file:

- `note` — `@note #<uid> observation : title` (UID auto-generated, editable)
- `note-observation` / `note-todo` / `note-issue` / `note-context` / `note-lesson` / `note-prompt` — same header with the category preset
- `author` — `@author name`
- `see` — `@see #id`
- `note-full` — complete note with directives and body

## Layout

```
grammar.js / src/ / queries/   tree-sitter grammar + highlight queries
lua/ plugin/ ftdetect/         Neovim integration (F3 in-comment highlighting,
                               LSP reference client in lua/devnotes/lsp.lua)
snippets/                      per-filetype LuaSnip wrappers (devnotes + host
                               filetypes: go, typescript, javascript, ...)
telescope/                     Telescope extension for the devnotes.list
                               pickers (optional; vim.ui.select is the fallback)
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
