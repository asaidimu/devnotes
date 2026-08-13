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
    // Fields are consumed greedily from the left; a header field can never
    // begin with ':', so the first WS ":" WS after the category is always
    // the title delimiter ("first occurrence" semantics, SPEC 7/15/27).
    header_line: $ => seq(
      '@note',
      $._ws,
      $.id,
      $._ws,
      $.category,
      repeat(seq($._ws, $.header_field)),
      $._ws,
      ':',
      $._ws,
      $.title,
      $._newline
    ),

    id: $ => token(/#[A-Za-z0-9_-]+/),
    category: $ => token(/[A-Za-z][A-Za-z0-9_-]*/),

    header_field: $ => choice(
      $.status,
      $.priority,
      $.timestamp,
      $.tags,
      $.extension_field
    ),

    status: $ => token(prec(PREC.keyword, choice(
      'open',
      'resolved',
      'wontfix',
      'deprecated'
    ))),

    priority: $ => token(prec(PREC.keyword, choice(
      'P0',
      'P1',
      'P2',
      'P3'
    ))),

    // Loose shape only; strict ISO 8601 validation happens in the semantic layer.
    timestamp: $ => token(
      /[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9]{2}:[0-9]{2}(:[0-9]{2})?([+-][0-9]{2}:[0-9]{2}|Z)?)?/
    ),

    tags: $ => seq(
      '#',
      $.tag_name,
      repeat(seq(',', optional($._ws), '#', $.tag_name))
    ),
    tag_name: $ => token(/[A-Za-z0-9_-]+/),

    extension_field: $ => token(/[A-Za-z][A-Za-z0-9_-]*/),

    title: $ => token(/[^\r\n]+/),

    // ------------------------------------------------------------------
    // DIRECTIVES
    // ------------------------------------------------------------------
    directive_line: $ => choice(
      $.author_directive,
      $.see_directive,
      $.extension_directive
    ),

    author_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'author')),
      $._ws,
      $.author_value,
      $._newline
    ),
    author_value: $ => token(/[^\r\n]+/),

    see_directive: $ => seq(
      '@',
      token(prec(PREC.keyword, 'see')),
      $._ws,
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
      optional(seq($._ws, $.directive_value)),
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
    // Single atomic token so CRLF can never be split between lines.
    _newline: $ => token(choice(seq('\r', '\n'), '\n')),
  }
});
