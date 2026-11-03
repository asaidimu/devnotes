# devnotes

A meta-language for developer notes, with a tree-sitter grammar, a Go
validation engine, and a Neovim integration.

This is a monorepo. The repo root is simultaneously:

- a **tree-sitter grammar** (`grammar.js`, `src/`, `queries/`)
- a **Neovim plugin** (`lua/`, `plugin/`, `ftdetect/`)
- a **Go module** (`go.mod`; packages under `engine/` and `bindings/go/`)

## Note syntax

```devnotes
@note #pool-ownership observation : Sub-document pool ownership
@status open
@priority high
@tags #pooling #decoders
@created 2026-08-07
@author jane
@see #document-lifecycle

Nested sub-documents need their own pools.
```

The header carries identity, category, and title. Metadata lives on directive
lines: `@status` (`open`/`resolved`/`wontfix`/`deprecated`), `@priority`
(`critical`/`high`/`medium`/`low`), `@tags`, `@created` (`@timestamp` is an
alias), `@author`, and `@see`. Legacy `P0`–`P3` values are accepted in
`@priority`, but canonical output uses the words.

Notes written in the old positional style can be rewritten in place:

```sh
devnotes migrate --dry-run   # preview
devnotes migrate             # rewrite legacy headers under --root (or paths)

Migration also canonicalizes `@status=open`-style directives (some models
emit `=` instead of a space) to `@status open`.
```

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
  In host files only comment regions containing a `@note` header are treated
  as DevNotes; ordinary comments are ignored. Pull diagnostics
  (`textDocument/diagnostic`, `workspace/diagnostic`) are served when the
  client supports them.
- **Hover** — full note preview (metadata, tags, authors, reference count,
  body excerpt, stale-status banner) and target previews on `@see` values.
- **Listing** — the `devnotes.list` command with structured filters
  (category/status/priority/tag/author/file/text/reference), sorting and
  pagination; `devnotes.get`, `devnotes.nextInFile`, `devnotes.prevInFile`,
  `devnotes.graph`, `devnotes.exportContext` and `devnotes.reindex` round out
  the command surface. The dedicated `:DevNotesSymbols` panel is built on
  this command.
- **Completion** — context-aware inside headers and directives: note IDs
  (proximity-ranked, `filterText` without `#`, preview via
  `completionItem/resolve`), tags with usage counts, categories (core +
  custom), statuses, named priorities, `@tags`/`@created` directives, a
  "today" timestamp and template snippets.
- **Code actions** — quick fixes for every fixable diagnostic, status and
  priority transitions with canonical insertion, `Assign to…` from config,
  `@see` add/remove/jump-to-target, and note/file canonical formatting.
- **Navigation & edits** — `definition`/`references` across the workspace,
  `documentLink` on every `@see`, workspace-wide `rename` with
  `prepareRename` (rejects ambiguous IDs), canonical `formatting` (safe
  inside Go/TS comments), `foldingRange`, `selectionRange`, `inlayHint` and
  `codeLens`. The server deliberately does not advertise
  `documentSymbol`/`workspace/symbol`: generic symbol UIs merge every client,
  so notes would pollute the host language's outline. Notes live in their own
  panel instead (see `:DevNotesSymbols`).
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

- `<C-p>` / `:DevNotesPreview` — floating preview of the note or reference
  under the cursor
- resting the cursor on a `#id` token shows the same preview after
  `updatetime`, IDE-style; disable with `hover_preview = false`
- `<leader>dca` — DevNotes-only code actions (status/priority/assign/format,
  plus "Jump to #id" on `@see` references)
- `:DevNotesList`, `:DevNotesListIssues`, `:DevNotesListTodos` — pickers over
  `devnotes.list` (vim.ui.select, or `:Telescope devnotes notes` with the
  Telescope extension)
- `:DevNotesSymbols` — the dedicated notes panel: a separate workspace
  outline that never mixes with the host language's symbols (see below)
- `:DevNotesNext` / `:DevNotesPrev` — cycle notes in the file
- `:DevNotesFormat` — explicit canonical formatting (never on save)
- `require('devnotes.statusline').component()` — current note's
  `category · status · priority` (priority as a word) for your statusline

#### Symbol window

`:DevNotesSymbols` opens a persistent notes window — every note in the
workspace (including notes embedded in Go/TS/JS comments), grouped and
syntax-highlighted, backed by `devnotes.list`. It is completely separate from
the host language's symbol provider, so gopls/tsserver outlines stay clean.
Open it as a floating window or docked to any side with a command argument
(`:DevNotesSymbols left|right|top|bottom|float`; `:DevNotesSymbolsToggle` /
`:DevNotesSymbolsClose` also exist). In the window:

- `<CR>` — jump to the note in its source window (floats close, splits stay open)
- `j` / `k` — move between note rows, skipping expanded details
- `Tab` or `za` — expand/collapse the note under the cursor (one note at a
  time; expanding another collapses the previous one)
- `p` / `K` — floating preview of the note body and metadata
- `r` — refresh; `q` — close
- each note is one row: a priority glyph (`▲` critical, `◆` high, `●` medium,
  `▽` low) plus its title, so long titles get the full panel width
- expanding a row wraps the full title and body (no ellipses), reflowing
  hard-wrapped source paragraphs and hanging list items so lines stay even;
  a footer with the `#id`, tags, `file:line`, priority, status, and reference
  count wraps onto continuation rows as needed
- categories are section headers (`✖ Issues (4)`) with the group icon and a
  count; groups are ordered by their most urgent item
- the focused note's `#id · priority · status · file:line` appears in the
  panel winbar, so the ID stays available while rows are collapsed
- the layout adapts to the panel width (the winbar sheds fields first), and
  keeps a right margin so wrapped text stops short of the window edge;
  expanded content and footer rows wrap instead of clipping, and resizing
  re-renders in place
- the list auto-refreshes on entering the window and after any `BufWritePost`

Default layout is a centered float; make it a docked outline via setup:

```lua
require('devnotes').setup({
  symbols = {
    style = 'left',        -- 'float' (default) | 'left' | 'right' | 'top' | 'bottom'
    width = 80,            -- columns for vertical splits / float width
    height = 24,           -- rows for horizontal splits / float height
    group_by = 'category', -- 'category' | 'file' | 'none'
    sort = 'priority',     -- passed through to devnotes.list
    show_resolved = true,  -- include resolved/deprecated notes
    margin = 4,            -- blank columns kept at the right edge
  },
})
```

#### Telescope

When telescope.nvim is installed the extension registers automatically
(`require('telescope').load_extension('devnotes')`; lazy-loaded setups get it
on the first `:DevNotesTelescope` call):

- `:Telescope devnotes notes` — all open notes (aliases: `list`)
- `:Telescope devnotes issues` — open issues (alias: `list_issues`)
- `:Telescope devnotes todos` — todos (alias: `list_todos`)
- `:Telescope devnotes references` — notes referencing the `#id` under the cursor
- `:Telescope devnotes symbols` — every note including resolved/deprecated

Each picker fuzzy-matches id/title/tags/path, shows the note body in the
preview pane and jumps to the note on `<CR>`. `:DevNotesTelescope
notes|issues|todos|references|symbols` is an equivalent dispatcher that works
even when telescope itself is lazy-loaded.

IDs are a tooling concern (unique + stable), titles are for humans — so the
`note` snippets auto-generate a short random UID for the `#id` slot.

### Snippets

The plugin registers LuaSnip snippets when LuaSnip is available (disable with
`require('devnotes').setup({ load_snippets = false, hover_preview = false })`). The same snippets load
for the `devnotes` filetype **and** for the host filetypes — `go`,
`typescript`, `typescriptreact`, `javascript`, `javascriptreact` — so you can
write notes directly inside Go/TS/JS comments without ever opening a `.dn`
file:

- `note` — `@note #<uid> observation : title` (UID auto-generated, editable)
- `note-observation` / `note-todo` / `note-issue` / `note-context` / `note-lesson` / `note-prompt` — same header with the category preset
- `status` / `priority` / `tags` / `created` — metadata directive snippets
- `author` — `@author name`
- `see` — `@see #id`
- `note-full` — complete note with directives and body

## Layout

```
grammar.js / src/ / queries/   tree-sitter grammar + highlight queries
lua/ plugin/ ftdetect/         Neovim integration (F3 in-comment highlighting,
                               LSP reference client in lua/devnotes/lsp.lua,
                               symbol window in lua/devnotes/symbols.lua)
snippets/                      per-filetype LuaSnip wrappers (devnotes + host
                               filetypes: go, typescript, javascript, ...)
lua/telescope/_extensions/     Telescope extension for the devnotes.list
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

See [todo/SPEC.md](todo/SPEC.md) for the DevNotes language and validation rules.
