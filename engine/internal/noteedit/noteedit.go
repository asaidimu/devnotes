// Package noteedit mutates DevNotes in place in their host source file.
//
// DevNotes has no separate "database": the comment in the source *is* the
// record. Every command that changes a note's state (adding one, claiming
// it, resolving it, reprioritizing it) edits the host file's comment text
// directly. The index (package index) is a derived, disposable cache that
// gets re-synced from source after each edit — it is never the target of a
// write on its own.
package noteedit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
)

// AddOptions describes a new note to insert.
type AddOptions struct {
	ID        string // optional; generated from Title if empty
	Category  string
	Status    string // optional; omitted from the header when "" or "open"
	Priority  string // optional, e.g. "P1"
	Timestamp string // optional, ISO 8601
	Tags      []string
	Title     string
	Body      string   // optional, may be multi-line
	Author    string   // optional, emits an @author directive
	See       []string // optional, each emits an @see directive
}

// FoundNote pairs a parsed note with the file it was found in, for callers
// that only have an ID and need to locate + edit it.
type FoundNote struct {
	File string
	Note model.Note
}

// commentStyle describes how to wrap a note line for a given host file.
type commentStyle struct {
	prefix string // e.g. "// "; empty for .dn files
	isDN   bool
}

func styleFor(ext string) commentStyle {
	switch ext {
	case ".dn", ".devnotes":
		return commentStyle{isDN: true}
	default:
		return commentStyle{prefix: "// "}
	}
}

// normalizeID strips a leading "#" so callers can pass IDs with or without
// it. model.Note.ID includes the "#" (it's part of the grammar's id
// token), but CLI users and index lookups work more naturally without it.
func normalizeID(id string) string {
	return strings.TrimPrefix(id, "#")
}

// GenerateID produces a short, stable, human-legible ID from a title, with
// a random suffix to avoid collisions ("fix-pool-leak-a1b2").
func GenerateID(title string) string {
	slug := slugify(title)
	if slug == "" {
		slug = "note"
	}
	if len(slug) > 32 {
		slug = strings.TrimRight(slug[:32], "-")
	}
	return slug + "-" + randHex(4)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// Find locates a note by ID across the given files, returning the first
// match. Callers with an index should prefer index.Entry.Location.File and
// call FindInFile directly; this is for ID-only lookups (e.g. from a CLI
// flag) against a candidate file set.
func Find(files []string, id string) (*FoundNote, error) {
	for _, f := range files {
		n, err := FindInFile(f, id)
		if err != nil {
			continue
		}
		if n != nil {
			return &FoundNote{File: f, Note: *n}, nil
		}
	}
	return nil, fmt.Errorf("note %q not found", id)
}

// FindInFile re-parses a single file and returns the note with the given
// ID, or nil if absent.
func FindInFile(file, id string) (*model.Note, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	res, err := pipeline.CheckFile(file, filepath.Ext(file), src)
	if err != nil {
		return nil, err
	}
	want := normalizeID(id)
	for _, loc := range res.NoteLocs {
		if normalizeID(loc.Note.ID) == want {
			n := loc.Note
			return &n, nil
		}
	}
	return nil, nil
}

// Add inserts a new note as a comment immediately before the given 1-based
// line number in file (line 0 or a value beyond EOF appends at end of
// file). Indentation is copied from the anchor line. It returns the
// generated or supplied note ID.
func Add(file string, beforeLine int, opts AddOptions) (string, error) {
	if opts.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if opts.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	id := normalizeID(opts.ID)
	if id == "" {
		id = GenerateID(opts.Title)
	}

	lines, nl, err := readLines(file)
	if err != nil {
		return "", err
	}
	style := styleFor(filepath.Ext(file))

	anchor := beforeLine - 1
	if anchor < 0 || anchor > len(lines) {
		anchor = len(lines)
	}
	indent := ""
	if anchor < len(lines) {
		indent = leadingWhitespace(lines[anchor])
	} else if anchor > 0 {
		indent = leadingWhitespace(lines[anchor-1])
	}

	block := buildNoteBlock(id, opts, style, indent)

	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:anchor]...)
	out = append(out, block...)
	out = append(out, lines[anchor:]...)

	if err := writeLines(file, out, nl); err != nil {
		return "", err
	}
	return id, nil
}

func buildNoteBlock(id string, opts AddOptions, style commentStyle, indent string) []string {
	header := "@note #" + id + " " + opts.Category
	if opts.Status != "" && opts.Status != "open" {
		header += " " + opts.Status
	}
	if opts.Priority != "" {
		header += " " + opts.Priority
	}
	if opts.Timestamp != "" {
		header += " " + opts.Timestamp
	}
	if len(opts.Tags) > 0 {
		tags := make([]string, len(opts.Tags))
		for i, t := range opts.Tags {
			t = strings.TrimPrefix(t, "#")
			tags[i] = "#" + t
		}
		header += " " + strings.Join(tags, ",")
	}
	header += " : " + opts.Title

	var lines []string
	wrap := func(text string) string {
		if style.isDN {
			return indent + text
		}
		return indent + style.prefix + text
	}
	lines = append(lines, wrap(header))
	if opts.Author != "" {
		lines = append(lines, wrap("@author "+opts.Author))
	}
	for _, s := range opts.See {
		lines = append(lines, wrap("@see "+s))
	}
	if opts.Body != "" {
		for _, bl := range strings.Split(opts.Body, "\n") {
			lines = append(lines, wrap(bl))
		}
	}
	return lines
}

// SetStatus rewrites a note's status field in place, inserting one if
// absent. status must be one of open/resolved/wontfix/deprecated.
func SetStatus(file, id, status string) error {
	return editHeaderField(file, id, "status", status, func(n *model.Note) (model.HeaderField, bool) {
		if n.HasStatus {
			for _, f := range n.Fields {
				if f.Kind == "status" {
					return f, true
				}
			}
		}
		return model.HeaderField{}, false
	})
}

// SetPriority rewrites a note's priority field in place, inserting one if
// absent. priority must be P0-P3, or "" to leave unset (no-op if already
// unset).
func SetPriority(file, id, priority string) error {
	if priority == "" {
		return nil
	}
	return editHeaderField(file, id, "priority", priority, func(n *model.Note) (model.HeaderField, bool) {
		if n.HasPri {
			for _, f := range n.Fields {
				if f.Kind == "priority" {
					return f, true
				}
			}
		}
		return model.HeaderField{}, false
	})
}

// editHeaderField replaces an existing header field's text (via its known
// range) or, if the field is absent, inserts " <value>" immediately after
// the category token on the header line. This is a text-level edit rather
// than a re-render of the whole header so unrelated formatting (metadata
// order, spacing) is left untouched.
func editHeaderField(file, id, kind, value string, existing func(*model.Note) (model.HeaderField, bool)) error {
	n, err := FindInFile(file, id)
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("note %q not found in %s", id, file)
	}
	lines, nl, err := readLines(file)
	if err != nil {
		return err
	}

	if field, ok := existing(n); ok {
		ln := int(field.Range.StartLine)
		if ln < 0 || ln >= len(lines) {
			return fmt.Errorf("field range out of bounds for %s", id)
		}
		line := lines[ln]
		sc, ec := int(field.Range.StartCol), int(field.Range.EndCol)
		if sc < 0 || ec > len(line) || sc > ec {
			return fmt.Errorf("field column range out of bounds for %s", id)
		}
		lines[ln] = line[:sc] + value + line[ec:]
		return writeLines(file, lines, nl)
	}

	// Not present: insert right after the category token on the header
	// line. The header line is the first line of the note's range.
	ln := int(n.Range.StartLine)
	if ln < 0 || ln >= len(lines) {
		return fmt.Errorf("header line out of bounds for %s", id)
	}
	line := lines[ln]
	idx := headerCategoryEnd(line, normalizeID(n.ID), n.Category)
	if idx < 0 {
		return fmt.Errorf("could not locate category token for %s in %s", id, file)
	}
	lines[ln] = line[:idx] + " " + value + line[idx:]
	return writeLines(file, lines, nl)
}

var wsRun = regexp.MustCompile(`\s+`)

// headerCategoryEnd finds the byte offset right after the category token
// on a "@note #id category ..." line, by locating "@note", skipping the
// id token, then the category token. Returns -1 if not found.
func headerCategoryEnd(line, id, category string) int {
	at := strings.Index(line, "@note")
	if at < 0 {
		return -1
	}
	rest := line[at+len("@note"):]
	// Skip whitespace, then the "#id" token.
	idTok := "#" + id
	idPos := strings.Index(rest, idTok)
	if idPos < 0 {
		return -1
	}
	after := rest[idPos+len(idTok):]
	// Skip whitespace before category.
	trimmed := strings.TrimLeft(after, " \t")
	skipped := len(after) - len(trimmed)
	if !strings.HasPrefix(trimmed, category) {
		return -1
	}
	base := at + len("@note") + idPos + len(idTok) + skipped
	return base + len(category)
}

// SetExtension sets (or replaces) an "@name value" extension directive
// immediately after the header line. Used for @assignee and other
// workspace-defined directives (SPEC 9.2 / extension_directive).
func SetExtension(file, id, name, value string) error {
	n, err := FindInFile(file, id)
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("note %q not found in %s", id, file)
	}
	for _, d := range n.ExtDirs {
		if d.Name == name {
			lines, nl, err := readLines(file)
			if err != nil {
				return err
			}
			ln := int(d.Range.StartLine)
			if ln < 0 || ln >= len(lines) {
				return fmt.Errorf("directive range out of bounds for %s", id)
			}
			indent := leadingWhitespace(lines[ln])
			style := styleFor(filepath.Ext(file))
			text := "@" + name + " " + value
			if style.isDN {
				lines[ln] = indent + text
			} else {
				lines[ln] = indent + style.prefix + text
			}
			return writeLines(file, lines, nl)
		}
	}
	// Not present: insert a new directive line right after the header line.
	lines, nl, err := readLines(file)
	if err != nil {
		return err
	}
	ln := int(n.Range.StartLine)
	if ln < 0 || ln >= len(lines) {
		return fmt.Errorf("header line out of bounds for %s", id)
	}
	indent := leadingWhitespace(lines[ln])
	style := styleFor(filepath.Ext(file))
	text := "@" + name + " " + value
	newLine := indent + text
	if !style.isDN {
		newLine = indent + style.prefix + text
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:ln+1]...)
	out = append(out, newLine)
	out = append(out, lines[ln+1:]...)
	return writeLines(file, out, nl)
}

// AppendBody appends one or more body lines to the end of a note's block,
// right before the block's last line. Used by "note resolve --body" to
// leave a closing note without disturbing the rest of the block.
func AppendBody(file, id, text string) error {
	n, err := FindInFile(file, id)
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("note %q not found in %s", id, file)
	}
	lines, nl, err := readLines(file)
	if err != nil {
		return err
	}
	// n.Range.EndLine is NOT reliable for this: it's an exclusive
	// "one past end" normalized position that the engine's SourceMap
	// clamps to the block's own last line only when the block happens to
	// be the last one in its contiguous comment region; when another
	// note block immediately follows (no blank line between), EndLine
	// instead points at the *next* block's header line (verified: adding
	// a second note directly after the first, with no separating blank
	// line, changed the first note's EndLine from "its own last line" to
	// "the second note's header line"). So instead of trusting EndLine,
	// re-derive the block's true last content line by scanning source
	// text from the header, stopping at the next "@note #..." header, a
	// blank line, or a line that's no longer a comment in this file's
	// style.
	style := styleFor(filepath.Ext(file))
	headerLine := int(n.Range.StartLine)
	if headerLine < 0 || headerLine >= len(lines) {
		return fmt.Errorf("header line out of bounds for %s", id)
	}
	lastContentLine := blockLastLine(lines, style, headerLine)
	insertAt := lastContentLine + 1
	indent := leadingWhitespace(lines[lastContentLine])
	var newLines []string
	for _, bl := range strings.Split(text, "\n") {
		if style.isDN {
			newLines = append(newLines, indent+bl)
		} else {
			newLines = append(newLines, indent+style.prefix+bl)
		}
	}
	out := make([]string, 0, len(lines)+len(newLines))
	out = append(out, lines[:insertAt]...)
	out = append(out, newLines...)
	out = append(out, lines[insertAt:]...)
	return writeLines(file, out, nl)
}

// reNewHeader matches a genuine new note header ("@note #id ..."), as
// opposed to a bare escaped "@note" appearing as literal body text
// (SPEC 24), which has no "#id" following it.
var reNewHeader = regexp.MustCompile(`^@note\s+#\S+`)

// blockLastLine re-derives the true last content line of the note block
// starting at headerLine, by scanning forward through the file's raw text
// rather than trusting the engine's (ambiguous, clamp-prone) Range.EndLine.
// It stops at the next "@note #..." header, a blank line, or a line that's
// no longer a comment in this file's style, and returns the last line
// still considered part of the block.
func blockLastLine(lines []string, style commentStyle, headerLine int) int {
	last := headerLine
	for i := headerLine + 1; i < len(lines); i++ {
		content, ok := stripMarker(lines[i], style)
		if !ok {
			break
		}
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			break
		}
		if reNewHeader.MatchString(trimmed) {
			break
		}
		last = i
	}
	return last
}

// stripMarker strips this file's comment marker from a line and reports
// whether the line is still part of a comment in that style. For .dn
// files (no marker), any non-blank line counts.
func stripMarker(line string, style commentStyle) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if style.isDN {
		return trimmed, true
	}
	if !strings.HasPrefix(trimmed, "//") {
		return "", false
	}
	return strings.TrimPrefix(trimmed, "//"), true
}

func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// readLines splits a file into lines without terminators, reporting the
// line-ending style ("\n" or "\r\n") so writeLines can round-trip it.
func readLines(file string) (lines []string, nl string, err error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, "", err
	}
	nl = "\n"
	s := string(b)
	if strings.Contains(s, "\r\n") {
		nl = "\r\n"
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	trailingNL := strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{}, nl, nil
	}
	lines = strings.Split(s, "\n")
	_ = trailingNL
	return lines, nl, nil
}

func writeLines(file string, lines []string, nl string) error {
	content := strings.Join(lines, nl)
	if content != "" {
		content += nl
	}
	info, err := os.Stat(file)
	perm := os.FileMode(0o644)
	if err == nil {
		perm = info.Mode()
	}
	return os.WriteFile(file, []byte(content), perm)
}
