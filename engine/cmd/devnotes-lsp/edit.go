// Text-edit computation: canonical note formatting, header token edits
// (status/priority), directive insertion, and quick-fix edits. All edits are
// comment-prefix aware so notes inside host-language comments are rewritten
// without touching surrounding code. Coordinates here are 0-based lines and
// byte columns; conversion to LSP UTF-16 positions happens at the boundary.
package main

import (
	"sort"
	"strings"
	"time"

	"github.com/asaidimu/devnotes/engine/internal/migrate"
	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/noteedit"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
)

// textEditB is a byte-coordinate text edit.
type textEditB struct {
	StartLine, StartCol, EndLine, EndCol uint
	NewText                              string
}

// lastLineOf returns the last line a note actually occupies. A mapped range
// that ends at column 0 ends exactly where the next line begins, so the end
// line is exclusive in that case.
func lastLineOf(n model.Note) int {
	end := int(n.Range.EndLine)
	if n.Range.EndCol == 0 && end > int(n.Range.StartLine) {
		end--
	}
	if end < int(n.Range.StartLine) {
		end = int(n.Range.StartLine)
	}
	return end
}

// docLines splits a document body into lines (without terminators).
func docLines(d *doc) [][]byte {
	src := d.text
	var out [][]byte
	start := 0
	for i, c := range src {
		if c == '\n' {
			out = append(out, src[start:i])
			start = i + 1
		}
	}
	if start < len(src) {
		out = append(out, src[start:])
	}
	return out
}

// prefixAt returns the comment prefix of the note's line at absolute line.
func (e *NoteEntry) prefixAt(line int) string {
	idx := line - int(e.N().Range.StartLine)
	if idx >= 0 && idx < len(e.Loc.Prefixes) {
		return e.Loc.Prefixes[idx]
	}
	return ""
}

// noteLayout locates the structural lines of a note in the file. Directives
// may appear anywhere at column 0, so they are tracked individually rather
// than as one contiguous block before a mandatory separator.
type noteLayout struct {
	header     int
	directives []int // sorted line numbers
	firstBody  int   // first body line, including a canonical blank separator
	bodyStart  int   // first non-blank body line
	lastBody   int
}

func noteLayoutFor(d *doc, e *NoteEntry) noteLayout {
	lines := docLines(d)
	start := int(e.N().Range.StartLine)
	end := lastLineOf(e.N())
	if end >= len(lines) {
		end = len(lines) - 1
	}
	l := noteLayout{header: start, firstBody: -1, bodyStart: -1, lastBody: -1}
	directive := map[int]bool{}
	for _, dr := range e.N().DirectiveRanges {
		ln := int(dr.StartLine)
		if ln > start && ln <= end {
			directive[ln] = true
			l.directives = append(l.directives, ln)
		}
	}
	sort.Ints(l.directives)
	for ln := start + 1; ln <= end; ln++ {
		if directive[ln] {
			continue
		}
		if l.firstBody < 0 {
			l.firstBody = ln
		}
		if l.bodyStart < 0 && !isBlankAfterPrefix(lines[ln], e.prefixAt(ln)) {
			l.bodyStart = ln
		}
		l.lastBody = ln
	}
	return l
}

func isBlankAfterPrefix(line []byte, prefix string) bool {
	if len(prefix) > len(line) {
		return false
	}
	return strings.TrimRight(string(line[len(prefix):]), " \t\r") == ""
}

// isDirectiveLine reports whether a source line holds a parsed directive.
// It replaces the old "before the separator" test now that directives may
// appear anywhere at column 0.
func isDirectiveLine(e *NoteEntry, line int) bool {
	for _, dr := range e.N().DirectiveRanges {
		if int(dr.StartLine) == line {
			return true
		}
	}
	return false
}

// noteMetaBody is the compatibility view used by folding. sepLine now means
// "first non-directive line", which is the nearest equivalent of the former
// separator. Callers that need exact directive positions should use
// noteLayoutFor or isDirectiveLine.
func noteMetaBody(d *doc, e *NoteEntry) (metaEnd, sepLine, bodyStart, bodyEnd int) {
	l := noteLayoutFor(d, e)
	metaEnd = l.header
	if len(l.directives) > 0 {
		metaEnd = l.directives[len(l.directives)-1]
	}
	return metaEnd, l.firstBody, l.bodyStart, l.lastBody
}

// canonicalMetadata renders the canonical metadata lines for a note:
// header, then core metadata directives, authors, deduplicated @see lines,
// then extension directives — each group preserving insertion order.
// Returns nil for notes whose header could not be parsed; those are left
// untouched by formatting.
func canonicalMetadata(e *NoteEntry) []string {
	n := e.N()
	if n.ID == "" || n.Category == "" || n.NoTitle {
		return nil
	}
	out := []string{"@note " + n.ID + " " + n.Category + " : " + n.Title}
	if n.HasStatus {
		out = append(out, "@status "+n.Status)
	}
	if n.HasPri {
		out = append(out, "@priority "+model.PriorityLabel(n.Priority))
	}
	if len(n.Tags) > 0 {
		tags := make([]string, len(n.Tags))
		for i, t := range n.Tags {
			tags[i] = "#" + t
		}
		out = append(out, "@tags "+strings.Join(tags, " "))
	}
	if n.HasTs {
		out = append(out, "@created "+n.Timestamp)
	}
	for _, a := range n.Authors {
		out = append(out, "@author "+a)
	}
	inline := map[model.Range]bool{}
	for _, r := range n.InlineRefs {
		inline[r.Range] = true
	}
	seen := map[string]bool{}
	for _, r := range n.Refs {
		if r.Value == "" || inline[r.Range] {
			continue // inline @see stays in the body
		}
		key := r.Kind + "\x00" + r.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, "@see "+r.Value)
	}
	for _, d := range n.ExtDirs {
		if d.Value != "" {
			out = append(out, "@"+d.Name+" "+d.Value)
		} else {
			out = append(out, "@"+d.Name)
		}
	}
	return out
}

// canonicalMetadataWithLegacy folds positional fields from a legacy header
// into the canonical metadata block. The parser only sees the canonical
// header, so formatting a legacy note with canonicalMetadata alone silently
// drops its status/priority/tags/date/extension tokens.
func canonicalMetadataWithLegacy(lines [][]byte, start int, e *NoteEntry) []string {
	meta := canonicalMetadata(e)
	if meta == nil || start < 0 || start >= len(lines) {
		return meta
	}
	h, ok := migrate.LegacyHeader(string(lines[start]))
	if !ok {
		return meta
	}
	// The parser's error recovery may assign the wrong category/title to a
	// legacy header, so rebuild the first line from the source text itself.
	meta[0] = "@note " + h.ID + " " + h.Category + " : " + h.Title
	seen := make(map[string]bool, len(meta)+len(h.Directives))
	out := make([]string, 0, len(meta)+len(h.Directives))
	out = append(out, meta[0])
	seen[meta[0]] = true
	for _, d := range append(h.Directives, meta[1:]...) {
		if !seen[d] {
			out = append(out, d)
			seen[d] = true
		}
	}
	return out
}

// formatNoteEdits computes the text edits that canonicalize one note:
// directives are hoisted into a canonical block after the header, and body
// lines only lose trailing whitespace. Inline @see references stay in place.
func formatNoteEdits(d *doc, e *NoteEntry) []textEditB {
	lines := docLines(d)
	start := int(e.N().Range.StartLine)
	meta := canonicalMetadataWithLegacy(lines, start, e)
	if meta == nil || start >= len(lines) {
		return nil
	}
	layout := noteLayoutFor(d, e)

	var out []textEditB
	prefix := e.prefixAt(start)

	// Rewrite only the header line.
	out = append(out, textEditB{
		StartLine: uint(start), StartCol: e.N().Range.StartCol,
		EndLine: uint(start), EndCol: uint(len(lines[start])),
		NewText: meta[0],
	})

	// Insert canonical directives immediately after the header.
	if len(meta) > 1 {
		var sb strings.Builder
		for _, m := range meta[1:] {
			sb.WriteString("\n")
			sb.WriteString(prefix)
			sb.WriteString(m)
		}
		out = append(out, textEditB{
			StartLine: uint(start), StartCol: uint(len(lines[start])),
			EndLine: uint(start), EndCol: uint(len(lines[start])),
			NewText: sb.String(),
		})
	}

	// Remove every original directive line; the canonical block replaces it.
	for _, ln := range layout.directives {
		if ln < len(lines) {
			out = append(out, removeLineEdit(ln))
		}
	}

	// Body lines only lose trailing whitespace.
	for ln := layout.firstBody; ln >= 0 && ln <= layout.lastBody && ln < len(lines); ln++ {
		prefixLen := len(e.prefixAt(ln))
		if prefixLen > len(lines[ln]) {
			continue
		}
		content := string(lines[ln][prefixLen:])
		trimmed := strings.TrimRight(content, " \t\r")
		if content != trimmed {
			out = append(out, textEditB{
				StartLine: uint(ln), StartCol: uint(prefixLen),
				EndLine: uint(ln), EndCol: uint(len(lines[ln])),
				NewText: trimmed,
			})
		}
	}
	return out
}

// insertDirectiveEdit inserts a directive after the last existing directive
// (or after the header). Directives may be interleaved with body text, so the
// first body line is no longer a reliable insertion anchor.
func insertDirectiveEdit(d *doc, e *NoteEntry, directive string) textEditB {
	lines := docLines(d)
	l := noteLayoutFor(d, e)
	ln := l.header
	if len(l.directives) > 0 {
		ln = l.directives[len(l.directives)-1]
	}
	if ln >= len(lines) {
		ln = len(lines) - 1
	}
	return textEditB{
		StartLine: uint(ln), StartCol: uint(len(lines[ln])),
		EndLine: uint(ln), EndCol: uint(len(lines[ln])),
		NewText: "\n" + e.prefixAt(ln) + directive,
	}
}

// fieldRangeOf returns the first header field of the given kind.
func fieldRangeOf(e *NoteEntry, kind string) (model.Range, bool) {
	for _, f := range e.N().Fields {
		if f.Kind == kind {
			return f.Range, true
		}
	}
	return model.Range{}, false
}

// replaceTokenEdit replaces a token range, consuming one preceding space so
// removals do not leave double whitespace.
func replaceTokenEdit(line int, r model.Range, newText string, lines [][]byte) textEditB {
	startCol := r.StartCol
	if startCol > 0 && int(startCol) < len(lines[line]) && lines[line][startCol-1] == ' ' && newText == "" {
		startCol--
	}
	return textEditB{
		StartLine: uint(line), StartCol: startCol,
		EndLine: uint(line), EndCol: r.EndCol,
		NewText: newText,
	}
}

// headerInsertCol finds the column right after the category token on the
// header line; edits that "insert a missing field" anchor there.
func headerInsertCol(e *NoteEntry, lines [][]byte) (line int, col uint, ok bool) {
	n := e.N()
	line = int(n.Range.StartLine)
	if line < 0 || line >= len(lines) {
		return 0, 0, false
	}
	// Canonical position for a new field: after the status token when
	// one exists, else directly after the category.
	if r, has := fieldRangeOf(e, "status"); has && int(r.StartLine) == line &&
		int(r.EndCol) <= len(lines[line]) && r.EndCol > r.StartCol {
		return line, r.EndCol, true
	}
	if n.CategoryRange.EndCol > n.CategoryRange.StartCol &&
		int(n.CategoryRange.EndCol) <= len(lines[line]) {
		return line, n.CategoryRange.EndCol, true
	}
	// Fallback: scan the line text ("@note #id category ...").
	idx := headerCategoryEnd(string(lines[line]), strings.TrimPrefix(n.ID, "#"), n.Category)
	if idx < 0 {
		return 0, 0, false
	}
	return line, uint(idx), true
}

// headerCategoryEnd mirrors noteedit's helper: byte offset after the
// category token on a header line.
func headerCategoryEnd(line, id, category string) int {
	at := strings.Index(line, "@note")
	if at < 0 {
		return -1
	}
	rest := line[at+len("@note"):]
	idTok := "#" + id
	idPos := strings.Index(rest, idTok)
	if idPos < 0 {
		return -1
	}
	after := rest[idPos+len(idTok):]
	trimmed := strings.TrimLeft(after, " \t")
	if category == "" || !strings.HasPrefix(trimmed, category) {
		return -1
	}
	return at + len("@note") + idPos + len(idTok) + (len(after) - len(trimmed)) + len(category)
}

// statusEdit computes the edit that sets a note's status, replacing an
// existing directive value or inserting a new @status directive.
func statusEdit(d *doc, e *NoteEntry, status string) (textEditB, bool) {
	lines := docLines(d)
	if r, ok := fieldRangeOf(e, "status"); ok {
		line := int(r.StartLine)
		if line < len(lines) {
			return replaceTokenEdit(line, r, status, lines), true
		}
	}
	return insertDirectiveEdit(d, e, "@status "+status), true
}

// priorityEdit computes the edit that sets (or clears) a note's priority.
// Removing a priority deletes the whole @priority directive line.
func priorityEdit(d *doc, e *NoteEntry, priority string) (textEditB, bool) {
	lines := docLines(d)
	if r, ok := fieldRangeOf(e, "priority"); ok {
		line := int(r.StartLine)
		if line < len(lines) {
			if priority == "" {
				return removeLineEdit(line), true
			}
			return replaceTokenEdit(line, r, model.PriorityLabel(priority), lines), true
		}
	}
	if priority == "" {
		return textEditB{}, false // nothing to clear
	}
	return insertDirectiveEdit(d, e, "@priority "+model.PriorityLabel(priority)), true
}

// assignEdit sets the @assign directive: replaces an existing line's value
// or inserts a new directive at the end of the directive block.
func assignEdit(d *doc, e *NoteEntry, author string) textEditB {
	for _, dir := range e.N().ExtDirs {
		if dir.Name == "assign" {
			lines := docLines(d)
			line := int(dir.Range.StartLine)
			if line < len(lines) {
				return textEditB{
					StartLine: uint(line), StartCol: dir.Range.StartCol,
					EndLine: uint(line), EndCol: dir.Range.EndCol,
					NewText: "@assign " + author,
				}
			}
		}
	}
	return insertDirectiveEdit(d, e, "@assign "+author)
}

// titlePlaceholderEdit inserts a TITLE placeholder for MISSING_TITLE.
func titlePlaceholderEdit(d *doc, e *NoteEntry) (textEditB, bool) {
	lines := docLines(d)
	line := int(e.N().Range.StartLine)
	if line < 0 || line >= len(lines) {
		return textEditB{}, false
	}
	prefixLen := len(e.prefixAt(line))
	content := string(lines[line][prefixLen:])
	trimmed := strings.TrimRight(content, " \t\r")
	col := prefixLen + len(trimmed)
	sep := " : "
	if strings.Contains(trimmed, ":") {
		sep = " "
	}
	return textEditB{
		StartLine: uint(line), StartCol: uint(col),
		EndLine: uint(line), EndCol: uint(col),
		NewText: sep + "TITLE",
	}, true
}

// removeTagEdit removes a tag token (including its '#') plus an adjacent
// separator so the remaining @tags directive stays parsable. When it is the
// last tag, the whole directive line is removed.
func removeTagEdit(d *doc, e *NoteEntry, r model.Range) (textEditB, bool) {
	lines := docLines(d)
	line := int(r.StartLine)
	if line < 0 || line >= len(lines) {
		return textEditB{}, false
	}
	if len(e.N().Tags) <= 1 {
		return removeLineEdit(line), true
	}
	hashStart := int(r.StartCol) - 1
	if hashStart < 0 {
		hashStart = 0
	}
	end := int(r.EndCol)
	switch {
	case hashStart > 0 && lines[line][hashStart-1] == ',':
		hashStart--
	case end < len(lines[line]) && lines[line][end] == ',':
		end++
		for end < len(lines[line]) && lines[line][end] == ' ' {
			end++
		}
	case end < len(lines[line]) && lines[line][end] == ' ':
		end++
	case hashStart > 0 && lines[line][hashStart-1] == ' ':
		hashStart--
	}
	return textEditB{StartLine: uint(line), StartCol: uint(hashStart),
		EndLine: uint(line), EndCol: uint(end), NewText: ""}, true
}

// removeLineEdit deletes an entire line (used for removing a dangling @see).
func removeLineEdit(line int) textEditB {
	return textEditB{StartLine: uint(line), StartCol: 0,
		EndLine: uint(line) + 1, EndCol: 0, NewText: ""}
}

// suggestedID generates a deduplicated ID for the INVALID_ID quick fix.
func suggestedID(e *NoteEntry, taken map[string]bool) string {
	base := noteedit.GenerateID(e.N().Title)
	for taken["#"+base] {
		base = noteedit.GenerateID(e.N().Title)
	}
	return "#" + base
}

// today returns the local date in YYYY-MM-DD form.
func today() string {
	return time.Now().Format("2006-01-02")
}

// editsToLSP converts byte-coordinate edits to LSP TextEdits for a doc.
func (d *doc) editsToLSP(edits []textEditB) []map[string]interface{} {
	lines := docLines(d)
	var out []map[string]interface{}
	for _, e := range edits {
		sc, ec := e.StartCol, e.EndCol
		if int(e.StartLine) < len(lines) {
			sc = pipeline.UTF16Col(lines[e.StartLine], e.StartCol)
		}
		if int(e.EndLine) < len(lines) {
			ec = pipeline.UTF16Col(lines[e.EndLine], e.EndCol)
		}
		out = append(out, map[string]interface{}{
			"range": map[string]interface{}{
				"start": map[string]interface{}{"line": e.StartLine, "character": sc},
				"end":   map[string]interface{}{"line": e.EndLine, "character": ec},
			},
			"newText": e.NewText,
		})
	}
	return out
}
