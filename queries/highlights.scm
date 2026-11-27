; DevNotes highlight queries
; Captures follow the tree-sitter standard capture list.
; Directive keywords and the '@' marker are token patterns rather than named
; nodes, so they are highlighted via their enclosing named nodes.

; ------------------------------------------------------------------
; Header
; ------------------------------------------------------------------
(header_line (id) @constant)
(header_line (category) @type)
(header_line (title) @string)

; ------------------------------------------------------------------
; Metadata directives
; ------------------------------------------------------------------
(status_directive) @keyword
(status_directive (status_value) @keyword)

(priority_directive) @keyword
(priority_directive (priority_value) @constant)

(tags_directive) @keyword
(tags_directive (tag_list (tag_name) @tag))

(created_directive) @keyword
(created_directive (timestamp_value) @constant)

; ------------------------------------------------------------------
; Directives
; ------------------------------------------------------------------
(author_directive) @keyword
(author_directive (author_value) @string)

(see_directive) @keyword
(see_directive (reference (url) @string.special))
(see_directive (reference (id) @constant))
(see_directive (reference (reference_value) @string))

(extension_directive) @function
(extension_directive (directive_name) @function)
(extension_directive (directive_value) @string)
