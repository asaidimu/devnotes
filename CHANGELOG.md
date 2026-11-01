# [0.3.1](https://github.com/asaidimu/devnotes/compare/v0.3.0...v0.3.1) (2026-11-01)


### Features

* **nvim:** symbol window — `:DevNotesSymbols[ left|right|top|bottom|float]`
  (+ Toggle/Close) opens a dedicated workspace-wide notes outline backed by
  `devnotes.list`: grouped by category or file, extmark-highlighted
  status/priority/category glyphs, `<CR>` jump, `p`/`K` body preview, `r`
  refresh, auto-refresh on enter/save; layout configurable via
  `require('devnotes').setup{ symbols = {...} }`
* **nvim:** Telescope extension moved to `lua/telescope/_extensions/devnotes.lua`
  so `require('telescope').load_extension('devnotes')` resolves on the
  runtimepath (the previous `telescope/devnotes.lua` location was unreachable
  by telescope); adds `references` (ID under cursor) and `symbols` (all
  statuses) pickers, note-body preview pane, and auto-registration; legacy
  `list`/`list_issues`/`list_todos` names kept as aliases
* **nvim:** `:DevNotesReferences` command and `:DevNotesTelescope
  notes|issues|todos|references|symbols` dispatcher that works with
  lazy-loaded telescope

### Bug Fixes

* **nvim:** `id_under_cursor()` no longer relies on an async documentSymbol
  request whose result never reached the caller; it is now a synchronous
  cursor-aware text scan

# [0.3.0](https://github.com/asaidimu/devnotes/compare/v0.2.0...v0.3.0) (2026-10-31)


### Features

* **lsp:** implement the DevNotes LSP Feature Specification v1.0 (LSP-3 conformance):
  clean human-readable diagnostics with relatedInformation + structured data,
  severity table alignment (DUPLICATE_ID → Warning, UNKNOWN_CATEGORY → Info),
  INVALID_HEADER recovery for unparseable notes, hover previews for notes and
  references, textDocument/definition, references, documentSymbol,
  workspace/symbol, documentLink, foldingRange, selectionRange, inlayHint,
  codeLens, pull diagnostics, code actions (quick fixes, status/priority
  transitions, assignment, @see editing, canonical formatting), workspace-wide
  rename with prepareRename, canonical formatting (comment-prefix aware),
  and the devnotes.list/get/graph/nextInFile/prevInFile/exportContext/reindex
  commands
* **lsp:** configuration via workspace/didChangeConfiguration and devnotes.toml
  (defaultAuthor, knownAuthors, custom categories, hover/list limits)
* **lsp:** proximity-ranked ID completion (same-file/same-dir/workspace tiers),
  tag and author completion, snippet templates, completionItem/resolve with
  note previews, filterText without the leading '#'
* **nvim:** reference LSP client — vim.lsp.enable wiring, <C-p> reference
  preview, :DevNotesList/ListIssues/ListTodos/Format/Next/Prev commands,
  vim.ui.select picker with Telescope extension, statusline component,
  @assign/@deadline snippets

### Bug Fixes

* **validate:** diagnostics no longer leak "SPEC n" section references; every
  message states the problem and how to fix it
* **model:** notes that fail to parse now produce INVALID_HEADER diagnostics
  instead of being dropped silently
* **validate:** INVALID_TAG/INVALID_PRIORITY/INVALID_ID/INVALID_DIRECTIVE
  checks added (duplicate tags, unknown priorities, malformed IDs)
* **pipeline:** note token ranges (ID, tags, authors, directives) are mapped
  to source coordinates and comment prefixes are preserved for edits

# [0.2.0](https://github.com/asaidimu/devnotes/compare/v0.1.1...v0.2.0) (2026-08-21)


### Features

* completion shows note title/category/status instead of file:line ([24106a9](https://github.com/asaidimu/devnotes/commit/24106a9994e84659bbe0b0ad7332c8fcd11a5c88))

## [0.1.1](https://github.com/asaidimu/devnotes/compare/v0.1.0...v0.1.1) (2026-08-20)


### Bug Fixes

* **lsp:** include metadata in note references ([fcda0dd](https://github.com/asaidimu/devnotes/commit/fcda0ddd528236ba6e1000684398df660863b115))
