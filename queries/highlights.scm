; DevNotes highlight queries
; Captures follow the tree-sitter standard capture list.

; ------------------------------------------------------------------
; Header
; ------------------------------------------------------------------
(header_line "@note" @keyword)
(header_line ":" @punctuation.delimiter)
(header_line (id) @constant)
(header_line (category) @type)
(header_line (title) @string)

; ------------------------------------------------------------------
; Metadata directives
; ------------------------------------------------------------------
(status_directive "@" @punctuation.delimiter)
(status_directive "status" @keyword)
(status_directive (status_value) @keyword)

(priority_directive "@" @punctuation.delimiter)
(priority_directive "priority" @keyword)
(priority_directive (priority_value) @constant)

(tags_directive "@" @punctuation.delimiter)
(tags_directive "tags" @keyword)
(tags_directive (tag_list (tag_name) @tag))

(created_directive "@" @punctuation.delimiter)
(created_directive (timestamp_value) @constant)

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
