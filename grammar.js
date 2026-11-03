const PREC = {
  keyword: 1,
};

export default grammar({
  name: 'devnotes',

  // No automatic whitespace skipping – we handle spaces and newlines
  // explicitly so that body whitespace is preserved byte-for-byte.
  extras: $ => [],

  // A bare "@note" body line can either continue the current note's body or
  // begin a new note; the header interpretation fails for a bare "@note" (no
  // whitespace + id follows), so the body interpretation always wins.
  conflicts: $ => [
    [$.note_block],
  ],

  rules: {
    // ------------------------------------------------------------------
    // FILE
    // ------------------------------------------------------------------
    source_file: $ => repeat($.note_block),

    // ------------------------------------------------------------------
    // NOTE
    // ------------------------------------------------------------------
    note_block: $ => seq(
      $.header_line,
      repeat($.directive_line),
      $.separator_line,
      repeat($.body_line)
    ),

    // ------------------------------------------------------------------
    // HEADER
    // ------------------------------------------------------------------
    // Header carries only identity, category and title. All metadata lives
    // in directives so values never need positional disambiguation.
    header_line: $ => seq(
      '@note',
      $._ws,
      $.id,
      $._ws,
      $.category,
      $._ws,
      ':',
      $._ws,
      $.title,
      $._newline
    ),

    id: $ => token(/#[A-Za-z0-9_-]+/),
    category: $ => token(/[A-Za-z][A-Za-z0-9_-]*/),

    // @tags accepts commas or whitespace between tags: "@tags #this #that".
    tag_list: $ => seq(
      '#',
      $.tag_name,
      repeat(seq(choice(seq(',', optional($._ws)), $._ws), '#', $.tag_name))
    ),
    tag_name: $ => token(/[A-Za-z0-9_-]+/),

    title: $ => token(/[^\r\n]+/),

    // ------------------------------------------------------------------
    // DIRECTIVES
    // ------------------------------------------------------------------
    directive_line: $ => choice(
      $.status_directive,
      $.priority_directive,
      $.tags_directive,
      $.created_directive,
      $.author_directive,
      $.see_directive,
      $.extension_directive
    ),

    // Values are deliberately unrestricted single tokens: semantic validation
    // reports INVALID_STATUS / INVALID_PRIORITY / INVALID_TIMESTAMP instead
    // of the grammar silently reinterpreting a bad value as an extension.
    status_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'status')),
      $._dsep,
      $.status_value,
      $._newline
    ),
    status_value: $ => token(/[^\r\n \t]+/),

    priority_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'priority')),
      $._dsep,
      $.priority_value,
      $._newline
    ),
    priority_value: $ => token(/[^\r\n \t]+/),

    tags_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'tags')),
      $._dsep,
      $.tag_list,
      $._newline
    ),

    created_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, choice('created', 'timestamp'))),
      $._dsep,
      $.timestamp_value,
      $._newline
    ),
    timestamp_value: $ => token(/[^\r\n \t]+/),

    author_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'author')),
      $._dsep,
      $.author_value,
      $._newline
    ),
    author_value: $ => token(/[^\r\n]+/),

    see_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'see')),
      $._dsep,
      $.reference,
      $._newline
    ),
    reference: $ => choice(
      $.id,
      $.url,
      $.reference_value
    ),
    url: $ => token(prec(1, /[A-Za-z][A-Za-z0-9+.-]*:[^\r\n\t ]+/)),
    reference_value: $ => token(/[^\r\n]+/),

    extension_directive: $ => seq(
      '@',
      $.directive_name,
      optional(seq($._dsep, $.directive_value)),
      $._newline
    ),
    directive_name: $ => token(/[A-Za-z0-9_-]+/),
    directive_value: $ => token(/[^\r\n]+/),

    // ------------------------------------------------------------------
    // SEPARATOR (the single blank line that terminates metadata)
    // ------------------------------------------------------------------
    separator_line: $ => seq(optional($._ws), $._newline),

    // ------------------------------------------------------------------
    // BODY
    // ------------------------------------------------------------------
    body_line: $ => choice(
      seq($.body_text, $._newline),
      seq(token('@note'), $._newline), // bare '@note' line (no header continues)
      $._newline
    ),

    // A body line is anything that does not start with "@note" followed by
    // whitespace. Lines starting with "@note " begin a new note (SPEC 4.2).
    // Higher token precedence so body text wins over the "@note" header
    // literal; a real header never matches these alternatives.
    body_text: $ => token(prec(2, choice(
      /[^@\r\n][^\r\n]*/,                                    // not starting with '@'
      /@(?:[^n\r\n]|n[^o\r\n]|no[^t\r\n]|not[^e\r\n]|note[^ \t\r\n])[^\r\n]*/, // '@' but not a '@note ' header
      /[\t ]+/                                               // whitespace-only line
    ))),

    // ------------------------------------------------------------------
    // COMMON
    // ------------------------------------------------------------------
    _ws: $ => /[ \t]+/,
    // Directives accept both canonical "@status open" and the common
    // "@status=open" spelling produced by some models. Formatting and
    // migration rewrite the latter to the canonical space-separated form.
    _dsep: $ => choice(
      $._ws,
      seq(optional($._ws), '=', optional($._ws))
    ),
    // Single atomic token so CRLF can never be split between lines.
    _newline: $ => token(choice(seq('\r', '\n'), '\n')),
  }
});
