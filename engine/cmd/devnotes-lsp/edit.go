// Text-edit computation: canonical note formatting, header token edits
// (status/priority), directive insertion, and quick-fix edits. All edits are
// comment-prefix aware so notes inside host-language comments are rewritten
// without touching surrounding code. Coordinates here are 0-based lines and
// byte columns; conversion to LSP UTF-16 positions happens at the boundary.
package main

import (
	"strings"
	"time"

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

// noteMetaBody locates the structural lines of a note in the file:
//
//	metaEnd  — last line of the metadata block (header + directives)
//	sepLine  — the blank separator line, or -1 when the separator exists
//	           only in normalized form (single-line host comment notes)
//	bodyStart/bodyEnd — inclusive body bounds, or -1 when there is no body
func noteMetaBody(d *doc, e *NoteEntry) (metaEnd, sepLine, bodyStart, bodyEnd int) {
	lines := docLines(d)
	start := int(e.N().Range.StartLine)
	end := lastLineOf(e.N())
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for ln := start + 1; ln <= end; ln++ {
		if isBlankAfterPrefix(lines[ln], e.prefixAt(ln)) {
			return ln - 1, ln, ln + 1, end
		}
	}
	return end, -1, -1, -1
}

func isBlankAfterPrefix(line []byte, prefix string) bool {
	if len(prefix) > len(line) {
		return false
	}
	rest := string(line[len(prefix):])
	return strings.TrimRight(rest, " \t\r") == ""
}

// canonicalMetadata renders the canonical metadata lines for a note (§8.6):
// header with ordered tokens, then authors, then deduplicated @see lines,
// then extension directives — each group preserving insertion order.
// Returns nil for notes whose header could not be parsed; those are left
// untouched by formatting.
func canonicalMetadata(e *NoteEntry) []string {
	n := e.N()
	if n.ID == "" || n.Category == "" || n.NoTitle {
		return nil
	}
	var b strings.Builder
	b.WriteString("@note " + n.ID + " " + n.Category)
	if n.HasStatus {
		b.WriteString(" " + n.Status)
	}
	if n.HasPri {
		b.WriteString(" " + n.Priority)
	}
	if n.HasTs {
		b.WriteString(" " + n.Timestamp)
	}
	if len(n.Tags) > 0 {
		tags := make([]string, len(n.Tags))
		for i, t := range n.Tags {
			tags[i] = "#" + t
		}
		b.WriteString(" " + strings.Join(tags, ","))
	}
	for _, f := range n.Ext {
		b.WriteString(" " + f.Value)
	}
	b.WriteString(" : " + n.Title)

	out := []string{b.String()}
	for _, a := range n.Authors {
		out = append(out, "@author "+a)
	}
	seen := map[string]bool{}
	for _, r := range n.Refs {
		if r.Value == "" {
			continue
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

// formatNoteEdits computes the text edits that canonicalize one note:
// the metadata block is rewritten, body lines only lose trailing whitespace.
func formatNoteEdits(d *doc, e *NoteEntry) []textEditB {
	meta := canonicalMetadata(e)
	if meta == nil {
		return nil
	}
	start := int(e.N().Range.StartLine)
	metaEnd, _, bodyStart, bodyEnd := noteMetaBody(d, e)
	lines := docLines(d)

	var out []textEditB
	var sb strings.Builder
	sb.WriteString(meta[0])
	for i := 1; i < len(meta); i++ {
		sb.WriteString("\n")
		sb.WriteString(e.prefixAt(start + i))
		sb.WriteString(meta[i])
	}
	endCol := uint(len(lines[metaEnd]))
	out = append(out, textEditB{
		StartLine: uint(start), StartCol: e.N().Range.StartCol,
		EndLine: uint(metaEnd), EndCol: endCol,
		NewText: sb.String(),
	})

	if bodyStart >= 0 {
		for ln := bodyStart; ln <= bodyEnd && ln < len(lines); ln++ {
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
	}
	return out
}

// insertDirectiveEdit inserts a directive line at the end of the note's
// directive block (before the separator when one exists in the file).
func insertDirectiveEdit(d *doc, e *NoteEntry, directive string) textEditB {
	metaEnd, sep, _, _ := noteMetaBody(d, e)
	lines := docLines(d)
	if sep >= 0 {
		return textEditB{
			StartLine: uint(sep), StartCol: 0,
			EndLine: uint(sep), EndCol: 0,
			NewText: e.prefixAt(sep) + directive + "\n",
		}
	}
	ln := metaEnd
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
// existing token in place or inserting one in canonical position.
func statusEdit(d *doc, e *NoteEntry, status string) (textEditB, bool) {
	lines := docLines(d)
	if r, ok := fieldRangeOf(e, "status"); ok {
		line := int(r.StartLine)
		if line < len(lines) {
			return replaceTokenEdit(line, r, status, lines), true
		}
	}
	line, col, ok := headerInsertCol(e, lines)
	if !ok {
		return textEditB{}, false
	}
	return textEditB{StartLine: uint(line), StartCol: col, EndLine: uint(line), EndCol: col,
		NewText: " " + status}, true
}

// priorityEdit computes the edit that sets (or clears) a note's priority.
func priorityEdit(d *doc, e *NoteEntry, priority string) (textEditB, bool) {
	lines := docLines(d)
	if r, ok := fieldRangeOf(e, "priority"); ok {
		line := int(r.StartLine)
		if line < len(lines) {
			return replaceTokenEdit(line, r, priority, lines), true
		}
	}
	if priority == "" {
		return textEditB{}, false // nothing to clear
	}
	line, col, ok := headerInsertCol(e, lines)
	if !ok {
		return textEditB{}, false
	}
	return textEditB{StartLine: uint(line), StartCol: col, EndLine: uint(line), EndCol: col,
		NewText: " " + priority}, true
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
// comma so the remaining tags field stays parsable.
func removeTagEdit(d *doc, e *NoteEntry, r model.Range) (textEditB, bool) {
	lines := docLines(d)
	line := int(r.StartLine)
	if line < 0 || line >= len(lines) {
		return textEditB{}, false
	}
	hashStart := int(r.StartCol) - 1
	if hashStart < 0 {
		hashStart = 0
	}
	end := int(r.EndCol)
	if hashStart > 0 && lines[line][hashStart-1] == ',' {
		hashStart--
	} else if end < len(lines[line]) && lines[line][end] == ',' {
		end++
		for end < len(lines[line]) && lines[line][end] == ' ' {
			end++
		}
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
