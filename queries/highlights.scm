; DevNotes highlight queries
; Captures follow the tree-sitter standard capture list.

; ------------------------------------------------------------------
; Header
; ------------------------------------------------------------------
(header_line "@note" @keyword)
(header_line ":" @punctuation.delimiter)
(header_line (id) @constant)
(header_line (category) @type)
(header_field (status) @keyword)
(header_field (priority) @constant)
(header_field (timestamp) @constant)
(header_field (extension_field) @constant)
(header_field (tags (tag_name) @tag))
(header_line (title) @string)

; ------------------------------------------------------------------
; Directives
; ------------------------------------------------------------------
(author_directive "@" @punctuation.delimiter)
(author_directive "author" @keyword)
(author_directive (author_value) @string)

(see_directive "@" @punctuation.delimiter)
(see_directive "see" @keyword)
(see_directive (reference (url) @string.special))
(see_directive (reference (id) @constant))
(see_directive (reference (reference_value) @string))

(extension_directive "@" @punctuation.delimiter)
(extension_directive (directive_name) @function)
(extension_directive (directive_value) @string)
