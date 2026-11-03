---
name: devnotes
description: Use the devnotes CLI to read, query, and manage DevNotes — structured @note comments embedded directly in source code (todos, issues, observations, decisions). Trigger this whenever the user's repo contains @note comments or a .devnotes/index.json file, when they ask to find/list/triage todos or issues "in the code" rather than in a separate tracker, when they ask you to leave a note/flag a bug/mark something resolved/assign a note while reading or writing code, or when they mention devnotes, note IDs (#some-id), or want a project-management view (backlog, report, who's assigned what) derived from source comments. Also trigger when writing a @note comment by hand (not via the CLI), since the grammar has a silent-data-loss trap (missing blank separator before body text) that's easy to hit without knowing it's there. Always check for a devnotes binary and a .devnotes/index.json before falling back to grep for @note comments by hand.
---

# devnotes

devnotes is project-management state (todos, issues, decisions, assignments)
living directly in source code as structured comments, plus a CLI that
validates, indexes, queries, and mutates them. Use it instead of grepping for
`TODO`/`FIXME` or hand-editing comments whenever the repo has it available.

## The one idea that explains everything

**The comment in the source file is the record of truth. Everything else is
derived.**

- `.devnotes/index.json` is a disposable cache built by scanning source.
  Delete it, run `devnotes index init`, get it back — no data loss.
- Every command that *changes* a note (`note add`, `note claim`, `note
  resolve`, `note status`, `note priority`) edits the actual comment in the
  `.go`/`.ts`/`.dn` file, then re-syncs just that file into the index.
- Every command that *reads* (`list`, `show`, `trace`, `report`) reads only
  the index — fast, no re-parsing, but can go stale if files changed outside
  devnotes. Run `devnotes index status` if results look wrong; `devnotes
  index update` to re-sync.

If you ever need to choose between editing the index file directly or using a
`devnotes note ...` command: always use the command. Hand-editing the index
does not touch the source comment, so the two will diverge.

## Notes grammar

A note is a comment block with this exact shape, in this exact order:

```
header_line
directive_line*      (@author, @see, or a custom @name — zero or more)
separator_line        <- one blank comment line, MANDATORY if body follows
body_line*            (free text, zero or more lines)
```

Read every part of that before writing or generating a note by hand — the
separator line in particular is a real trap, covered below.

### Header line

```
@note #<id> <category> : <title>
```

- `#<id>` — `#` followed by letters/digits/`_`/`-`. Referenced elsewhere with
  or without the leading `#`; devnotes CLI commands accept both forms.
- `<category>` — `observation`, `todo`, `issue`, `context`, `lesson`,
  `prompt`, or any custom identifier (custom categories parse fine but get a
  `warning`-level `UNKNOWN_CATEGORY` diagnostic from `devnotes check`).
- `: <title>` — **the first literal ` : ` (space-colon-space) after the
  category starts the title.** Everything after that, including further
  colons, is title text:
  ```
  @note #x observation : Why A : B is necessary
  ```
  title is `Why A : B is necessary`, not `Why A`. An empty title is a
  `MISSING_TITLE` error.

The header carries identity and category only. Status, priority, timestamps,
tags, authors, and references are **directive lines immediately after the
header**, one value per line:

```
@status open                    # open | resolved | wontfix | deprecated
@priority high                  # critical | high | medium | low
@tags #pooling #memory          # space- or comma-separated
@created 2026-08-20             # ISO 8601; @timestamp is an alias
@author jane
@see #other-id                  # or a bare URL
@name value                     # custom directive
```

- Status defaults to `open` when omitted.
- Canonical priorities are `critical` (P0), `high` (P1), `medium` (P2), and
  `low` (P3). The legacy aliases `P0`–`P3` are accepted in `@priority` for
  migration, but tools emit the words. Priority is optional.
- Repeating a core directive (`@status`, `@priority`, `@tags`, `@created`)
  is a `DUPLICATE_FIELD` error.
- Directive order is not enforced, but canonical formatting emits
  `@status`, `@priority`, `@tags`, `@created`, then authors/references.
- Some generators write `=` instead of a space (`@status=open`,
  `@tags=#a,#b`). That parses and is normalized to the canonical form; do not
  rewrite those lines by hand.

### Directive lines — and the ordering rule that silently reclassifies text

```
@see #other-id        (or a bare URL)
@author name
@name value            (custom directive — preserved, flagged UNKNOWN_DIRECTIVE at info level)
```

**Directives are only recognized in the block right after the header, before
any body text.** Once a body line has appeared, a later `@see`, `@author`, or
`@status` line is *not* parsed as a directive — it's just body text starting
with `@`. This isn't a bug, it's the grammar (`note_block` is `header_line,
directive_line*, separator_line, body_line*` — directives can't appear after
the separator). Practical effect: if `devnotes trace <id>` isn't finding a
reference you can plainly see in the comment, check whether the `@see` line
comes before or after the free-text body. `devnotes check` does **not** flag
this — a misplaced directive is still grammatically valid body text, so
nothing looks wrong to the validator.

### Diagnostics (`devnotes check`)

| Code | Severity | Meaning |
|---|---|---|
| `MISSING_TITLE` | error | title is empty after the delimiter |
| `INVALID_STATUS` | error | `@status` value isn't one of the four valid values |
| `INVALID_TIMESTAMP` | error | `@created`/`@timestamp` value doesn't parse as the expected shape |
| `DUPLICATE_FIELD` | error | same core directive (status/priority/tags/created) given twice |
| `DUPLICATE_ID` | error | same `#id` used by more than one note in the workspace |
| `UNKNOWN_CATEGORY` | warning | category isn't one of the built-in set |
| `UNRESOLVED_REFERENCE` | warning | `@see #id` points at an ID that doesn't exist anywhere |
| `INVALID_PRIORITY` | error | `@priority` value isn't a canonical word or `P0`–`P3` alias |
| `UNKNOWN_DIRECTIVE` | info | a custom `@name value` directive (e.g. `@assignee`) — expected and harmless, just informational |

Note what's *not* in this table: there is no code for a misplaced directive
or a missing separator line. Both are silent as far as `check` is concerned.

## Setup

```
devnotes index init          # first time in a repo, or after cloning
devnotes index status        # is the index stale vs. the working tree?
devnotes index update        # re-sync (whole repo, or pass specific paths)
```

Global flags on every command: `--root <dir>` (default `.`), `--index <path>`
(default `.devnotes/index.json`), `--json` (machine-readable output instead
of text — use this when you're going to parse the result programmatically
rather than show it to the user).

If a command says "loading index... run `devnotes index init` first", that's
literal — do that, then retry.

## Reading the backlog

```
devnotes list                                   # everything
devnotes list --category=todo --status=open
devnotes list --priority=critical --assignee=alice
devnotes list --group-by=status                 # or category|priority|assignee
devnotes show <id>                              # full detail: body, refs, extensions, location
devnotes trace <id> --direction=out             # what this note references
devnotes trace <id> --direction=in              # what references this note
devnotes trace <id> --direction=both --depth=3
devnotes report --by=priority                   # counts, for a PM-style overview
```

`trace` only follows references shaped `@see #id` — bare-URL `@see` targets
have nothing to walk to, so they're skipped in the graph. And remember: a
`@see` written after body text isn't a reference at all (see Grammar above),
so `trace` won't find it either — that's correct behavior given what was
actually parsed, not a bug in `trace`.

Prefer `list`/`report` over re-grepping the codebase for `@note`: the index
is already built, filtering is instant, and you get structured fields
(status, priority, assignee) instead of raw text you'd have to re-parse.

## Capturing and updating notes

```
devnotes note add <file> --category=todo --title="..." \
  [--line=N] [--priority=high] [--status=open] [--tags=a,b] \
  [--body="..."] [--author=name] [--see=#other-id,...] [--id=custom-id]
```

- `--line` is 1-based, and means "insert *before* this line." Omit it to
  append at end of file.
- Category and title are the only required flags.
- If you don't pass `--id`, one is generated from the title
  (`fix-pool-leak-a1b2`).
- The blank separator line (see Grammar above) is inserted automatically
  when `--body` is given — you don't need to think about it here.

```
devnotes note claim <id> --assignee=name       # writes @assignee into source
devnotes note resolve <id> [--body="closing note"]
devnotes note status <id> <open|resolved|wontfix|deprecated>
devnotes note priority <id> <critical|high|medium|low>   # P0-P3 also accepted
```

All four mutation commands look up which file the note lives in via the
index automatically. If the index is missing or stale, pass `--file <path>`
explicitly rather than re-indexing just to run one command.

## Matching the command to the situation

**Reading code and want to flag something without breaking flow** (a
reviewer spotting a bug, an experimenter noting an observation): `devnotes
note add <file> --line=<N> --category=issue --title="..."` right at the line
in question. Low ceremony — category and title are the only things you must
decide.

**Picking up work** (a todo or issue assigned to you, or that you're about
to fix): `devnotes list --category=todo --status=open` to find it, `devnotes
show <id>` for full context, `devnotes trace <id>` to pull in anything it
references before touching the code, `devnotes note claim <id>
--assignee=you` to mark it, then `devnotes note resolve <id> --body="..."`
when done.

**Flagging problems from CI/QA**: `devnotes note add` with every flag
supplied (no interactive prompts, so it's script-safe), then `devnotes check
.` as the actual gate — `check` is the validation command (diagnostics table
above), separate from indexing. A CI job typically runs both: `check` to
fail the build on malformed notes, `list --status=open --priority=critical --json`
to fail the build on unresolved criticals.

**Getting a PM-style view**: `devnotes report --by=status` or `--by=assignee`
for counts; `devnotes list --group-by=category` for a scannable backlog.
`note claim` is the assignment mechanism — there's no separate "assign"
concept, claiming a note *is* assigning it.

## Other gotchas worth knowing

- **IDs**: the engine's internal representation includes the `#`
  (`#pool-owner`), but every CLI flag/arg accepts the ID with or without it.
  Don't worry about which form to use.
- **`note add` with no `--line`** appends at the literal end of the file with
  no blank-line separation from whatever precedes it. Valid, just a little
  visually rough — pass `--line` if you want it placed precisely.
- **Stale index**: `list`/`show`/`trace`/`report` never re-scan source
  automatically. If someone hand-edited a note comment outside devnotes (or
  you're looking at a repo you just pulled), run `devnotes index update`
  first, or you'll be querying stale data.
- **`check` vs. the index are independent.** `check` re-parses fresh from
  source every time and never touches `.devnotes/index.json`. It's the
  thing to run in CI; it does not require an index to exist.

## Command reference (quick lookup)

| Command | Reads/writes | Purpose |
|---|---|---|
| `check [paths...]` | source only | diagnostics (see table above) |
| `migrate [paths...]` | writes source | rewrite legacy positional headers to directives (`--dry-run` to preview) |
| `index init` | writes index | full workspace scan |
| `index update [paths...]` | source → index | incremental re-sync, hash-diffed |
| `index status` | reads both | stale/missing/untracked files |
| `note add <file>` | writes source, then index | capture a new note |
| `note claim <id>` | writes source, then index | set `@assignee` |
| `note resolve <id>` | writes source, then index | status → resolved, optional closing body |
| `note status <id> <status>` | writes source, then index | any lifecycle transition |
| `note priority <id> <critical|high|medium|low>` | writes source, then index | reprioritize (`P0`-`P3` accepted) |
| `list` | reads index | filtered listing |
| `show <id>` | reads index | full detail on one note |
| `trace <id>` | reads index | walk the `@see` graph |
| `report` | reads index | counts by category/status/priority/assignee |

Every command supports `--help` for the exact current flag set if this
reference and the binary ever drift.
