# DevNotes Meta-Language Specification v3.0

*Human- and machine-readable annotations embedded in source-code comments*

---

## Abstract

DevNotes is a human- and machine-readable annotation language embedded in source-code comments.

It allows developers and AI agents to leave structured observations, tasks, issues, lessons, design context, prompts, and other engineering knowledge directly alongside source code.

DevNotes is designed to be consumed by:

* developers reading source code;
* AI coding agents;
* source indexers;
* LSP servers;
* IDEs and editor extensions;
* documentation generators;
* code-review and analysis tools.

DevNotes deliberately separates **note syntax** from **host-language comment syntax**. A DevNotes parser operates on normalized comment content supplied by a host-language adapter.

The language is designed around four principles:

1. **Human readability** — a note should look natural inside source code.
2. **Deterministic parsing** — equivalent notes must have unambiguous interpretations.
3. **Stable identity** — notes can be referenced independently of their source location.
4. **Safe extensibility** — future metadata must not make existing notes ambiguous.

---

# 1. Language Model

A DevNote consists of:

1. a header;
2. zero or more metadata directives;
3. a separator;
4. a Markdown body.

Conceptually:

```text
DevNote
├── Header
│   ├── @note
│   ├── ID
│   ├── Category
│   ├── Metadata
│   └── Title
├── Metadata Directives
│   ├── @author
│   └── @see
├── Separator
└── Body
```

A DevNote has a stable logical identity (`id`) and a physical source location.

The source location is **not** part of the note's identity. Moving a note to another file or line does not change its ID.

---

# 2. Host-Language Integration

## 2.1 Responsibility of the host adapter

DevNotes itself does not define programming-language comment syntax.

A host-language adapter is responsible for:

1. identifying comments;
2. extracting comment content;
3. identifying line-comment and block-comment regions;
4. normalizing block-comment decoration;
5. providing source locations.

The DevNotes parser operates only on normalized comment content.

For example, these may all expose the same DevNotes content:

```go
// @note #pool observation : Pool ownership
```

```python
# @note #pool observation : Pool ownership
```

```sql
-- @note #pool observation : Pool ownership
```

and:

```c
/*
 * @note #pool observation : Pool ownership
 */
```

The comment syntax belongs to the host adapter, not the DevNotes grammar.

---

# 3. Lexical Conventions

## 3.1 Character encoding

DevNotes source is interpreted as Unicode text.

Implementations SHOULD interpret source files as UTF-8 unless the host language or file encoding explicitly specifies otherwise.

Unicode characters are permitted in:

* titles;
* bodies;
* author identifiers;
* tag names where permitted by implementation-defined extensions.

The core grammar uses ASCII for structural tokens.

---

## 3.2 Whitespace

The following characters are treated as horizontal whitespace:

```text
SPACE
TAB
```

A newline terminates a logical DevNotes line.

Whitespace in the header is generally insignificant around structural fields.

Whitespace in the body is significant.

In particular:

```text
//     indented text
```

must preserve the indentation after comment-prefix normalization.

---

## 3.3 Case sensitivity

The following are case-sensitive:

* note IDs;
* tag names;
* author identifiers;
* URLs;
* category names;
* metadata values unless explicitly defined otherwise.

Core keywords are lowercase:

```text
@note
@author
@see
```

Core status values are lowercase:

```text
open
resolved
wontfix
deprecated
```

Priority values use uppercase:

```text
P0
P1
P2
P3
```

---

# 4. Note Discovery

## 4.1 Candidate detection

A host adapter identifies a DevNotes candidate when normalized comment content begins with:

```text
@note
```

followed by whitespace.

The following is therefore a DevNotes header:

```text
@note #abc observation : Something
```

The following is not:

```text
@notebook
```

Nor:

```text
@noteable
```

---

## 4.2 Note boundaries

A DevNote consists of a contiguous sequence of normalized comment lines belonging to the same host-language comment region.

For line comments:

* the note begins at a line containing a valid `@note` header;
* metadata directives immediately following the header belong to that note;
* the separator marks the beginning of the body;
* body lines continue while they remain part of the same contiguous comment region;
* a new `@note` header after the separator starts a new note.

Therefore, an `@note` appearing in a body is **not body text**. It begins a new note.

To include the literal text `@note` in a body, implementations SHOULD support escaping:

```text
\@note
```

The escape is removed when interpreting the body.

---

## 4.3 Block comments

For block comments:

```text
/*
 * @note ...
 *
 * body
 */
```

the complete block is available to the DevNotes parser.

A second `@note` occurring after a separator begins a new DevNote only if the host adapter exposes the block as multiple logical comment regions.

Otherwise, a block comment is treated as one host comment and may contain only one DevNote.

Implementations SHOULD warn when multiple DevNote headers occur inside a single block comment.

---

# 5. Block-Comment Normalization

Before DevNotes parsing, block comments are normalized.

Given:

```c
/*
 * @note #example observation : Example
 *
 *     indented body
 */
```

the host adapter removes:

1. the opening delimiter;
2. the closing delimiter;
3. the common decorative prefix.

The resulting logical lines are:

```text
@note #example observation : Example

    indented body
```

---

## 5.1 Decorative prefix

A decorative prefix is typically:

```text
*
```

optionally surrounded by whitespace.

For example:

```text
 * text
```

has decorative prefix:

```text
 *
```

Implementations SHOULD remove a common decorative prefix only when it is shared by all non-delimiter interior lines.

Source indentation that is not part of the common decorative prefix MUST be preserved.

---

## 5.2 Blank lines

After normalization, a blank line is a line containing zero or more whitespace characters.

For example:

```text
```

and:

```text
     
```

are both blank.

---

# 6. Syntax

## 6.1 Canonical form

The canonical DevNote syntax is:

```text
@note <id> <category> [metadata...] : <title>
@<directive> <value>
@<directive> <value>

<body>
```

Example:

```go
// @note #pool-ownership observation P1 #pooling,#decoders : Sub-document pool ownership
// @author jane
// @see #document-lifecycle
//
// The current design ties Document.Release() to a single pool.
// Nested sub-documents need their own pools.
```

---

# 7. Header

The header has the following logical structure:

```text
@note
<id>
<category>
<metadata>
:
<title>
```

The `:` sequence is the title delimiter.

The first occurrence of the exact delimiter:

```text
 : 
```

after the category terminates metadata parsing.

The title may contain additional colons:

```text
@note #x observation : Why A : B is necessary
```

The title is:

```text
Why A : B is necessary
```

---

# 8. Note ID

## 8.1 Syntax

A core note ID begins with `#`:

```text
#pool-release
#a1
#agent-20260810-1
#api_42
```

Grammar:

```abnf
id = "#" 1*(ALPHA / DIGIT / "-" / "_")
```

---

## 8.2 Identity

The ID uniquely identifies a note within a DevNotes namespace.

A workspace SHOULD use globally unique IDs.

Tools MUST detect duplicate IDs within the indexing scope.

Recommended IDs include:

```text
#pool-release
#api-pagination
#agent-20260810-1
```

For larger workspaces, namespaced IDs are RECOMMENDED through a project convention:

```text
#api/pagination
#db/migrations/42
#frontend/auth
```

Because `/` is not part of the core ID grammar, namespaced IDs are an extension and MUST be explicitly enabled by the implementation or workspace.

---

# 9. Category

## 9.1 Core categories

DevNotes defines the following core categories:

```text
observation
todo
issue
context
lesson
prompt
```

### observation

A factual observation about the current implementation or behavior.

### todo

A task that remains to be performed.

### issue

A known defect, risk, inconsistency, or problem.

### context

Background information or design rationale required to understand the surrounding code.

### lesson

Knowledge learned from implementation, debugging, or investigation.

### prompt

A reusable instruction or task intended primarily for an AI agent.

---

## 9.2 Custom categories

Implementations MAY support custom categories.

Custom categories SHOULD use lowercase ASCII identifiers:

```text
architecture
decision
constraint
experiment
```

Tools MUST preserve unknown categories rather than rejecting the entire note.

---

# 10. Header Metadata

The core header metadata fields are:

| Field     | Required | Default |
| --------- | -------: | ------- |
| status    |       no | `open`  |
| priority  |       no | none    |
| timestamp |       no | none    |
| tags      |       no | empty   |

Metadata fields are unordered.

Example:

```text
@note #x issue P1 open 2026-08-10 #security,#input : Possible XSS
```

is equivalent to:

```text
@note #x issue open #security,#input P1 2026-08-10 : Possible XSS
```

---

# 11. Status

The core statuses are:

```text
open
resolved
wontfix
deprecated
```

If omitted:

```text
status = open
```

Semantics:

### open

The note remains active.

### resolved

The condition or task described by the note has been addressed.

### wontfix

The note is intentionally not being addressed.

### deprecated

The note remains historically relevant but should no longer be treated as active guidance.

---

# 12. Priority

Priority is optional.

The core priorities are:

```text
P0
P1
P2
P3
```

Recommended semantics:

| Priority | Meaning                        |
| -------- | ------------------------------ |
| P0       | Critical; immediate attention  |
| P1       | High importance                |
| P2       | Normal importance              |
| P3       | Low importance / informational |

Priority does not imply severity.

An implementation MAY define additional severity metadata independently.

---

# 13. Timestamp

The timestamp uses ISO 8601.

Examples:

```text
2026-08-10
2026-08-10T16:29
2026-08-10T16:29Z
2026-08-10T16:29+03:00
```

A timestamp records the time associated with creation or observation of the note.

It does not represent source-file modification time.

If absent, the semantic value is `null`.

Tools MAY display file modification time or version-control information separately.

---

# 14. Tags

Tags are comma-separated identifiers.

Canonical syntax:

```text
#pooling,#performance,#memory
```

Whitespace after commas is permitted:

```text
#pooling, #performance, #memory
```

Each tag MUST begin with `#`.

Grammar:

```abnf
tags = tag *("," [WS] tag)
tag  = "#" tag-name
tag-name = 1*(ALPHA / DIGIT / "-" / "_")
```

Therefore:

```text
#pooling,decoders
```

is invalid in v3.

The canonical form is:

```text
#pooling,#decoders
```

Implementations MAY offer a compatibility mode for v2-style tags.

---

# 15. Title

The title is a required, single-line summary.

It begins after the first exact:

```text
 : 
```

delimiter.

Leading and trailing whitespace is removed.

Example:

```text
@note #x observation P2 : Current cache is process-local
```

The title is:

```text
Current cache is process-local
```

Titles MUST NOT contain newlines.

---

# 16. Metadata Directives

Metadata directives occur after the header and before the body separator.

Core directives:

```text
@author <identifier>
@see <reference>
```

Unknown directives are permitted.

---

# 17. Author

Syntax:

```text
@author <identifier>
```

Examples:

```text
@author jane
@author jane-doe
@author ai-code-reviewer-v2
```

Multiple authors MAY be represented by repeated directives:

```text
@author jane
@author bob
```

Repeated `@author` directives are preferred over comma-separated author strings because they are structurally unambiguous.

The `@author` directive identifies the creator or originator of the note.

It does not automatically identify the person who last modified it.

Version-control metadata SHOULD be used for modification history.

---

# 18. References

Syntax:

```text
@see <reference>
```

A reference may be:

1. another DevNote ID;
2. an absolute URL.

Examples:

```text
@see #document-lifecycle
@see https://github.com/example/project/issues/42
```

Multiple references are permitted:

```text
@see #document-lifecycle
@see #pool-ownership
@see https://example.com/design
```

Implementations SHOULD resolve note IDs when an index is available.

Unresolvable references MUST NOT make the note invalid.

They SHOULD produce a diagnostic.

---

# 19. Unknown Metadata

Unknown metadata directives are valid.

For example:

```text
@assign backend
@deadline 2026-12-31
@component storage
```

A parser MUST preserve unknown directives.

A tool MAY:

* index them;
* expose them to consumers;
* warn about unsupported directives;
* interpret them through a registered extension.

Unknown metadata MUST NOT alter the interpretation of core fields.

This is the primary extensibility mechanism of DevNotes.

---

# 20. Metadata Ordering

Metadata directives may appear in any order.

Example:

```text
@see #api
@author jane
@deadline 2026-12-31
```

is valid.

The separator terminates metadata:

```text
```

After the separator, all content is body content.

---

# 21. Metadata Validation

Each core metadata field may occur at most once unless explicitly defined otherwise.

Therefore this is invalid:

```text
@note #x issue P1 P2 : Something
```

and this is invalid:

```text
@note #x issue open resolved : Something
```

Unknown directives MAY repeat.

For example:

```text
@see #a
@see #b
```

is valid.

Implementations SHOULD report:

* duplicate core fields;
* malformed field values;
* unknown categories;
* invalid IDs;
* invalid priorities;
* invalid timestamps.

These diagnostics SHOULD identify the exact source range.

---

# 22. Body

The body is Markdown text.

The body begins after the metadata separator.

Example:

```text
@note #pool observation : Pool ownership

The current design ties `Document.Release()` to a single pool.

Nested documents require independent ownership.

- arrays need their own pools;
- records may contain nested allocations;
- releasing the parent must not invalidate children.
```

The body is otherwise unrestricted.

It may contain:

* Markdown;
* code blocks;
* lists;
* headings;
* links;
* tables;
* indented examples;
* natural-language text.

---

# 23. Body Whitespace

Body whitespace MUST be preserved after host-comment normalization.

For example:

```text
//     indented
//       more indented
```

produces:

```text
    indented
      more indented
```

This allows Markdown constructs and embedded code examples to retain their intended formatting.

---

# 24. Literal `@note` in Bodies

Because `@note` is structurally significant, literal occurrences SHOULD be escaped:

```text
\@note
```

The parser interprets:

```text
\@note
```

as:

```text
@note
```

in the semantic body.

An implementation MAY provide additional escaping rules, but the core language defines only this escape.

---

# 25. Empty Bodies

A note may have an empty body.

Example:

```text
// @note #remove-api todo P1 : Remove deprecated API
//
```

The body is the empty string.

A note is therefore valid without descriptive text after the separator.

---

# 26. Formal Grammar

The following grammar defines the core syntax after host-comment normalization.

```abnf
note-block      = header-line *(directive-line) separator-line *body-line

header-line     = "@note" WS id WS category *(WS header-field) WS ":" WS title

directive-line  = author-line / see-line / extension-line

author-line     = "@author" WS value
see-line        = "@see" WS reference
extension-line  = "@" directive-name [WS value]

separator-line  = WS?

body-line       = body-text

id              = "#" 1*(ALPHA / DIGIT / "-" / "_")

category        = identifier

header-field    = status / priority / timestamp / tags

status          = "open"
                / "resolved"
                / "wontfix"
                / "deprecated"

priority        = "P" ("0" / "1" / "2" / "3")

timestamp       = iso-date
                / iso-date "T" iso-time
                / iso-date "T" iso-time timezone

tags            = tag *("," [WS] tag)

tag             = "#" tag-name

tag-name        = 1*(ALPHA / DIGIT / "-" / "_")

directive-name  = 1*(ALPHA / DIGIT / "-" / "_")

reference       = id / url

url             = scheme ":" 1*url-char

scheme          = ALPHA *(ALPHA / DIGIT / "+" / "-" / ".")

title           = 1*title-char

value           = 1*(VCHAR / SP / HTAB)

body-text       = *(VCHAR / SP / HTAB)

identifier      = ALPHA *(ALPHA / DIGIT / "-" / "_")

WS              = 1*(SP / HTAB)
```

The ISO 8601 productions are intentionally referenced rather than completely reproduced in ABNF. Implementations MUST use ISO 8601-compatible validation.

---

# 27. Header Parsing Algorithm

A parser MUST NOT classify metadata by “most likely” shape.

Instead, it MUST parse fields deterministically.

Given:

```text
@note #id category <fields> : title
```

the parser performs the following steps:

1. Verify `@note`.
2. Parse the ID.
3. Parse the category.
4. Locate the first exact `:` delimiter.
5. Treat everything between category and delimiter as metadata tokens.
6. Parse each metadata token according to the core field grammar.
7. Reject duplicate core fields.
8. Preserve unknown tokens as extension metadata where possible.
9. Parse the title as the remainder after the delimiter.

This eliminates the ambiguity present in shape-based detection.

If a token could legally represent multiple core fields, the note is invalid rather than heuristically interpreted.

---

# 28. Canonical Serialization

Tools that rewrite DevNotes SHOULD serialize them in canonical form.

Recommended order:

```text
@note <id> <category> [status] [priority] [timestamp] [tags] : <title>
@author ...
@see ...

<body>
```

For example:

```text
// @note #pool observation open P1 2026-08-10 #pooling,#memory : Pool ownership
// @author jane
// @see #allocator
//
// The pool must remain valid until all nested documents are released.
```

Canonical serialization is not required for parsing.

Equivalent metadata orderings MUST retain the same semantic meaning.

---

# 29. Source Location

Every parsed note SHOULD expose:

```text
file
startLine
startColumn
endLine
endColumn
```

The source location is metadata about the representation, not the note itself.

Moving the note MUST NOT change its ID.

Indexers SHOULD maintain both:

```text
logical identity
physical location
```

as separate concepts.

---

# 30. Note Data Model

A parsed DevNote SHOULD be representable as:

```typescript
interface DevNote {
  id: string;
  category: string;

  status: NoteStatus;
  priority?: NotePriority;
  timestamp?: string;

  tags: string[];

  title: string;
  body: string;

  authors: string[];
  references: string[];

  metadata: Record<string, unknown>;

  location?: SourceLocation;
}

type NoteStatus =
  | "open"
  | "resolved"
  | "wontfix"
  | "deprecated";

type NotePriority =
  | "P0"
  | "P1"
  | "P2"
  | "P3";

interface SourceLocation {
  file: string;
  startLine: number;
  startColumn: number;
  endLine: number;
  endColumn: number;
}
```

The exact API is implementation-defined.

The important distinction is that:

* core metadata is typed;
* extensions are preserved;
* source location is separate;
* note identity is stable.

---

# 31. Error Model

Implementations SHOULD distinguish at least these diagnostic classes:

```text
INVALID_HEADER
INVALID_ID
INVALID_CATEGORY
INVALID_FIELD
DUPLICATE_FIELD
INVALID_STATUS
INVALID_PRIORITY
INVALID_TIMESTAMP
INVALID_TAG
MISSING_TITLE
INVALID_DIRECTIVE
UNRESOLVED_REFERENCE
DUPLICATE_ID
UNKNOWN_CATEGORY
UNKNOWN_DIRECTIVE
```

Diagnostics SHOULD contain:

```text
severity
code
message
source range
```

Severity SHOULD be one of:

```text
error
warning
info
```

---

# 32. Error Recovery

DevNotes tooling is intended for source-code environments where malformed annotations should not prevent the rest of the file from being indexed.

Therefore parsers SHOULD recover from malformed notes.

For example:

```text
@note broken ...
```

should produce a diagnostic while allowing subsequent notes to be discovered.

An invalid note MUST NOT cause the parser to discard unrelated notes in the same file.

---

# 33. Versioning

DevNotes syntax is versioned independently of the specification document.

A host or workspace MAY declare a DevNotes version.

Recommended project-level declaration:

```text
devnotes: "3"
```

The declaration is outside individual notes and is interpreted by tooling.

Individual notes SHOULD NOT normally contain a version declaration.

If a future DevNotes version introduces incompatible syntax, a workspace-level declaration allows tooling to select the appropriate parser.

---

# 34. Compatibility

A v3 parser MAY support a compatibility mode for v2 notes.

Compatibility mode MAY accept:

```text
#pooling,decoders
```

as equivalent to:

```text
#pooling,#decoders
```

Compatibility behavior MUST be explicit.

A tool MUST NOT silently reinterpret malformed v3 syntax under compatibility rules unless compatibility mode is enabled.

---

# 35. ID Resolution

When resolving:

```text
@see #some-note
```

tools SHOULD search the configured DevNotes namespace.

Resolution priority SHOULD be:

1. exact workspace ID;
2. configured project namespace;
3. local-file scope, if supported.

If multiple notes have the same ID, resolution is ambiguous and MUST produce a diagnostic.

---

# 36. Cross-References

References form a directed graph between notes.

For example:

```text
#problem
   |
   +--> #decision
             |
             +--> #implementation
```

Tools MAY expose this graph to:

* IDEs;
* documentation generators;
* AI agents;
* code-review systems.

Cycles are legal.

For example:

```text
#architecture
   -> #constraint
   -> #architecture
```

A cycle MUST NOT make the notes invalid.

---

# 37. AI-Agent Semantics

DevNotes is particularly suitable for AI-assisted development.

AI-generated notes SHOULD identify their origin through `@author`.

Example:

```text
// @note #agent-20260810-1 issue open P1 #security : Possible XSS
// @author ai-code-reviewer-v2
//
// The `comment` field is rendered without escaping.
// Input should be escaped before rendering.
```

AI agents SHOULD NOT automatically mark an issue as `resolved` merely because they proposed a solution.

A note's lifecycle should reflect the state of the code, not the confidence of the agent.

---

# 38. Recommended AI Categories

AI tooling MAY use additional categories such as:

```text
decision
constraint
hypothesis
experiment
risk
question
```

These are not core categories and must remain ordinary extensible categories.

---

# 39. Prompts

The `prompt` category is intended for reusable instructions embedded near relevant code.

Example:

```text
// @note #review-memory prompt #memory,#performance : Review allocation behavior
// @author ai-reviewer
//
// Review this implementation for:
// - unnecessary allocations;
// - ownership violations;
// - synchronization overhead.
//
// Do not optimize without evidence.
```

Prompt bodies are ordinary Markdown.

Tools MAY expose them as agent instructions, but SHOULD NOT automatically execute them merely because they exist in source code.

Execution policy belongs to the consuming tool.

---

# 40. Security Considerations

DevNotes may contain instructions intended for AI agents.

Therefore AI tooling MUST treat DevNotes as untrusted repository content unless the repository is explicitly trusted.

In particular:

* a `prompt` note MUST NOT automatically override system or user instructions;
* external URLs MUST NOT automatically be fetched;
* secrets MUST NOT be inferred from note contents;
* `@see` references MUST NOT automatically execute actions;
* instructions in notes MUST be treated as repository context, not privileged commands.

DevNotes is metadata embedded in source code; it is not an authorization mechanism.

---

# 41. Indexing

An indexer SHOULD store:

```text
id
category
status
priority
timestamp
tags
authors
references
title
body
file
source range
extension metadata
```

Recommended indexes include:

```text
ID
category
status
priority
tag
author
file
reference
```

This enables queries such as:

```text
all open P0/P1 issues
all notes tagged #performance
all notes authored by an AI agent
all notes referencing #allocator
all notes in src/storage
```

---

# 42. LSP Integration

An LSP implementation MAY provide:

* syntax highlighting;
* diagnostics;
* hover information;
* note navigation;
* `@see` navigation;
* ID completion;
* tag completion;
* category completion;
* status transitions;
* priority editing;
* note search;
* source-to-note references.

Hover information SHOULD display at minimum:

```text
category
status
priority
author
title
```

---

# 43. IDE Integration

An IDE MAY represent notes as annotations attached to source locations.

Recommended visual distinctions include:

```text
observation → informational
todo        → actionable
issue       → diagnostic
context     → contextual
lesson      → educational
prompt      → agent-oriented
```

Visual presentation is implementation-defined.

The underlying note semantics MUST remain independent of presentation.

---

# 44. Export and Prompt Generation

Tools MAY export DevNotes into:

* Markdown;
* JSON;
* JSONL;
* databases;
* agent context;
* documentation;
* issue trackers.

When generating AI context, tools SHOULD preserve:

1. note ID;
2. category;
3. status;
4. priority;
5. title;
6. body;
7. relevant references;
8. source location.

Resolved or deprecated notes SHOULD normally be excluded from active agent context unless explicitly requested.

---

# 45. Relationship to Version Control

DevNotes are source artifacts and SHOULD normally be version-controlled with the source code.

Version control provides information DevNotes deliberately does not attempt to duplicate, including:

* modification history;
* diffs;
* commit identity;
* blame;
* merge history.

The `@author` field represents semantic authorship of the note, not source-file ownership.

---

# 46. Relationship to Source Location

A DevNote MAY move:

```text
src/a.ts:42
```

to:

```text
src/b.ts:108
```

without changing:

```text
#my-note
```

Therefore:

```text
ID ≠ source location
```

Tools MUST use IDs for logical references.

Source locations SHOULD be treated as mutable indexing information.

---

# 47. Examples

## 47.1 Human observation

```go
// @note #pool-ownership observation open P1 2026-08-07 #pooling,#decoders : Sub-document pool ownership
// @author jane
// @see #document-lifecycle
//
// The current design ties Document.Release() to a single pool.
//
// Nested sub-documents need their own pools. Otherwise heavily used
// sub-pools can exhaust the shared pool.
func (d *Document) Release() { ... }
```

---

## 47.2 Agent-generated issue

```python
# @note #agent-20260810-1 issue open P2 2026-08-10 #security : Possible XSS in user input
# @author ai-code-reviewer-v2
#
# The `comment` field is rendered without escaping in the template.
# Escape the value before rendering.
def render_comment(comment):
    ...
```

---

## 47.3 Context note

```rust
/*
 * @note #vec-context context P3 2026-08-07 #performance,#ownership : Why Vec<u8> is used
 * @see #buffer-lifetime
 * @see https://doc.rust-lang.org/std/vec/struct.Vec.html
 *
 * Ownership constraints require an owned buffer.
 *
 * A borrowed slice would require lifetimes to remain valid across
 * asynchronous boundaries.
 */
void process(void *data, size_t len) { ... }
```

---

## 47.4 Todo

```java
// @note #remove-deprecated-api todo P1 2026-08-10 : Remove deprecated API
// @author admin
//
```

---

## 47.5 Multiple references

```text
// @note #storage-design decision #storage,#architecture : Use append-only records
// @author architecture-team
// @see #storage-recovery
// @see #storage-compaction
//
// Append-only records simplify recovery and make historical state observable.
```

---

## 47.6 Extension metadata

```text
// @note #cache-risk issue P1 #cache : Cache invalidation race
// @author jane
// @component cache
// @deadline 2026-12-31
// @assign backend
//
// Invalidation can race with concurrent writes.
```

The three extension directives are preserved even if a parser does not understand them.

---

# 48. Canonical Minimal Note

The smallest valid DevNote is:

```text
@note #x observation : Example
```

A body is optional.

The smallest useful embedded representation is therefore:

```text
// @note #x observation : Example
//
```

---

# 49. Canonical Full Note

A fully populated note may look like:

```text
// @note #pool-ownership issue open P1 2026-08-10T16:29+03:00 #pooling,#memory : Nested documents can outlive their parent pool
// @author ai-code-reviewer-v2
// @see #document-lifecycle
// @see #allocator-ownership
//
// ## Problem
//
// Nested documents currently share the parent's allocation pool.
//
// ## Consequence
//
// Releasing the parent can invalidate allocations still referenced by
// nested documents.
//
// ## Recommendation
//
// Give each independently releasable document ownership of its own pool.
//
// This should be verified against the lifecycle contract before implementation.
```

---

# 50. Design Guarantees

A conforming DevNotes implementation MUST guarantee:

1. **Deterministic core parsing.**
2. **Stable note identity through source movement.**
3. **Preservation of unknown metadata.**
4. **Preservation of body whitespace after normalization.**
5. **Explicit distinction between metadata and body.**
6. **No heuristic interpretation of ambiguous core fields.**
7. **Duplicate detection for core metadata.**
8. **Graceful recovery from malformed notes.**
9. **Resolvable references when an index is available.**
10. **Separation of host-language parsing from DevNotes parsing.**

---

# 51. Non-Goals

DevNotes does not define:

* programming-language syntax;
* a universal comment parser;
* a database format;
* an issue tracker;
* an authentication mechanism;
* an AI execution environment;
* a workflow engine;
* version-control semantics;
* a mandatory rendering format.

These belong to consuming tools.

---

# 52. Conformance Levels

Implementations MAY advertise one of three conformance levels.

## Level 1 — Parser

Supports:

* core header;
* core fields;
* directives;
* body;
* normalization.

## Level 2 — Indexer

Everything in Level 1 plus:

* stable ID indexing;
* reference resolution;
* duplicate detection;
* source locations;
* search/filtering.

## Level 3 — Tooling

Everything in Level 2 plus one or more:

* LSP;
* IDE integration;
* AI context generation;
* documentation generation;
* lifecycle management.

A Level 3 implementation MUST NOT change the core semantics of notes.

---

# 53. Future Extensions

Future versions MAY introduce:

```text
@assign
@deadline
@depends
@implements
@related
@decision
@confidence
@severity
```

Extensions SHOULD be implemented as directives rather than additional shape-based header tokens whenever practical.

This preserves deterministic parsing and prevents future fields from colliding with existing fields.

New core syntax MUST be introduced only through a new language version when backward compatibility cannot be guaranteed.

---

# 54. Rationale

The central design decision in DevNotes is that the annotation should remain pleasant to write manually:

```text
@note #id category : title
```

while everything beyond the minimal form remains machine-addressable.

The language therefore keeps:

* short IDs;
* human-readable categories;
* compact status and priority;
* Markdown bodies;
* explicit directives;
* lightweight references.

At the same time, the specification deliberately avoids heuristic parsing.

A parser should know what a token means from the grammar, not guess.

Likewise, extensibility should not depend on adding increasingly clever token-shape detection. Unknown directives can simply be preserved and interpreted by tools that understand them.

This gives DevNotes a stable core while leaving substantial room for project- and tool-specific conventions.

---

# 55. Summary of Core Syntax

The canonical form is:

```text
@note <id> <category> [status] [priority] [timestamp] [tags] : <title>
@author <identifier>
@see <reference>

<body>
```

with:

```text
id         = #identifier
category   = identifier
status     = open | resolved | wontfix | deprecated
priority   = P0 | P1 | P2 | P3
timestamp  = ISO 8601
tags       = #tag,#tag,...
title      = single-line text
body       = Markdown
```

The smallest useful note is:

```text
// @note #x observation : Example
```

The fundamental semantic model is:

```text
DevNote
│
├── stable identity
├── classification
├── lifecycle
├── metadata
├── provenance
├── references
├── title
├── Markdown body
└── source location
```

**End of specification v3.0**
