// Interactive handlers: completion (parsed-context with textEdit, filterText
// and snippet support), code actions (quick fixes + lifecycle refactors),
// rename with workspace-wide reference updates, and canonical formatting.
package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
)

var validRenameRe = regexp.MustCompile(`^#[A-Za-z0-9_-]+$`)

// stripCommentPrefix removes host-language comment markers from the start of
// a line fragment so completion works uniformly in .dn files and in Go/TS/JS
// comments. DevNotes '#' characters are never stripped.
func stripCommentPrefix(p string) string {
	s := strings.TrimLeft(p, " \t")
	for {
		switch {
		case strings.HasPrefix(s, "//"):
			s = strings.TrimPrefix(s, "//")
		case strings.HasPrefix(s, "/*"):
			s = strings.TrimPrefix(s, "/*")
		case strings.HasPrefix(s, "*"):
			s = strings.TrimPrefix(s, "*")
		default:
			return strings.TrimLeft(s, " \t")
		}
	}
}

func addLSPItem(items []map[string]interface{}, label string, kind int, detail, insert string) []map[string]interface{} {
	item := map[string]interface{}{"label": label, "kind": kind}
	if detail != "" {
		item["detail"] = detail
	}
	if insert != "" {
		item["insertText"] = insert
	}
	return append(items, item)
}

// tokenSpanBeforeCursor finds the start of the token being typed: the last
// '#' in p if present, else the end of p.
func tokenSpanBeforeCursor(p string, byteCol uint) (tokStart uint, hasHash bool) {
	if idx := strings.LastIndexByte(p, '#'); idx >= 0 {
		return uint(idx), true
	}
	return byteCol, false
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func (s *server) completionItems(uri string, line, char uint) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil {
		return nil
	}
	lineBytes := docLineAt(d, line)
	byteCol := utf16ToByte(lineBytes, char)
	if byteCol > uint(len(lineBytes)) {
		byteCol = uint(len(lineBytes))
	}
	p := stripCommentPrefix(string(lineBytes[:byteCol]))

	entries := s.allEntries()
	e := findEntryAt(d, line, byteCol)

	// ---- Parsed note context -------------------------------------------
	if e != nil {
		_, sep, _, _ := noteMetaBody(d, e)
		if sep >= 0 && int(line) >= sep {
			return nil // body or separator: no completion inside a note body
		}
		n := e.N()
		if int(line) == int(n.Range.StartLine) {
			return s.headerCompletion(uri, d, entries, e, p, line, byteCol, lineBytes)
		}
		// Directive block.
		return s.directiveCompletion(uri, d, entries, e, p, line, byteCol)
	}

	// ---- Text fallback (no parsed note at the cursor) -------------------
	lower := strings.ToLower(p)
	switch {
	case p == "":
		if s.snippetsAllowed(d, entries) {
			return s.snippetItems(entries)
		}
		return nil
	case strings.Contains(lower, "@note") && !strings.Contains(lower, "@author") && !strings.Contains(lower, "@see"):
		return s.headerCompletion(uri, d, entries, nil, p, line, byteCol, lineBytes)
	case strings.HasPrefix(p, "@see"):
		if idx := strings.LastIndexByte(p, '#'); idx >= 0 {
			partial := strings.TrimPrefix(p[idx+1:], "#")
			return s.idCompletion(uri, d, entries, partial, uint(idx), line, byteCol, nil)
		}
		return addLSPItem(nil, "@see", 14, "directive", "@see ")
	case strings.HasPrefix(p, "@author"):
		return s.authorCompletion(entries, afterLastSpace(p))
	case strings.HasPrefix(p, "#"):
		partial := strings.TrimPrefix(p[1:], "#")
		return s.idCompletion(uri, d, entries, partial, 0, line, byteCol, nil)
	case strings.HasPrefix(p, "@"):
		return s.directiveItems()
	}
	return nil
}

// headerCompletion serves the inside-a-header contexts: ID, tags, field
// keywords, or nothing once the title colon has been typed.
func (s *server) headerCompletion(uri string, d *doc, entries []NoteEntry, e *NoteEntry, p string, line, byteCol uint, lineBytes []byte) []map[string]interface{} {
	lower := strings.ToLower(p)
	if strings.Contains(lower, "@note") && strings.Contains(p, ":") {
		return nil // title is free text
	}
	// Parsed tags-field position beats every heuristic: the cursor sits on
	// (or right after) a tag token of the containing note.
	if e != nil {
		if r, ok := fieldRangeOf(e, "tags"); ok && int(line) == int(r.StartLine) &&
			byteCol >= r.StartCol && byteCol <= r.EndCol+2 {
			return s.tagCompletion(uri, d, entries, e, p, line, byteCol)
		}
	}
	// Tag context: a comma closer to the cursor than the last space.
	if lastComma := strings.LastIndexByte(p, ','); lastComma > strings.LastIndexByte(p, ' ') {
		return s.tagCompletion(uri, d, entries, e, p, line, byteCol)
	}
	if idx := strings.LastIndexByte(p, '#'); idx >= 0 {
		partial := strings.TrimPrefix(p[idx+1:], "#")
		return s.idCompletion(uri, d, entries, partial, uint(idx), line, byteCol, noteOf(e))
	}
	var items []map[string]interface{}
	if e != nil {
		// Mid-header: categories/statuses/priorities/timestamp per §9.4/9.5.
		items = s.fieldKeywordItems(entries)
		items = addLSPItem(items, "@author", 14, "directive", "@author ")
		items = addLSPItem(items, "@see", 14, "directive", "@see ")
		return items
	}
	// Start of a fresh header: the full template set plus field keywords.
	if s.snippetsAllowed(d, entries) {
		items = s.snippetItems(entries)
	} else {
		items = addLSPItem(items, "@note", 14, "note header", "@note #id category : title")
		items = addLSPItem(items, "@author", 14, "directive", "@author ")
		items = addLSPItem(items, "@see", 14, "directive", "@see ")
	}
	return append(items, s.fieldKeywordItems(entries)...)
}

// directiveCompletion serves positions inside a note's directive block.
func (s *server) directiveCompletion(uri string, d *doc, entries []NoteEntry, e *NoteEntry, p string, line, byteCol uint) []map[string]interface{} {
	switch {
	case strings.HasPrefix(p, "@see"):
		if idx := strings.LastIndexByte(p, '#'); idx >= 0 {
			partial := strings.TrimPrefix(p[idx+1:], "#")
			return s.idCompletion(uri, d, entries, partial, uint(idx), line, byteCol, noteOf(e))
		}
		return addLSPItem(nil, "@see", 14, "directive", "@see ")
	case strings.HasPrefix(p, "@author"):
		return s.authorCompletion(entries, afterLastSpace(p))
	case strings.HasPrefix(p, "#"):
		partial := strings.TrimPrefix(p[1:], "#")
		return s.idCompletion(uri, d, entries, partial, 0, line, byteCol, noteOf(e))
	case strings.HasPrefix(p, "@"):
		return s.directiveItems()
	default:
		return s.directiveItems()
	}
}

func noteOf(e *NoteEntry) *model.Note {
	if e == nil {
		return nil
	}
	n := e.N()
	return &n
}

func afterLastSpace(p string) string {
	if idx := strings.LastIndexByte(p, ' '); idx >= 0 {
		return p[idx+1:]
	}
	if idx := strings.LastIndexByte(p, '\t'); idx >= 0 {
		return p[idx+1:]
	}
	return ""
}

// fieldKeywordItems offers categories, statuses, priorities and "today".
func (s *server) fieldKeywordItems(entries []NoteEntry) []map[string]interface{} {
	var items []map[string]interface{}
	for _, c := range []string{"observation", "todo", "issue", "context", "lesson", "prompt"} {
		items = addLSPItem(items, c, 14, "category", c)
	}
	for _, cat := range sortedCustomCategories(customCategoryCounts(entries)) {
		items = addLSPItem(items, cat, 12, "custom category", cat)
	}
	for st, doc := range statusDocs {
		items = append(items, map[string]interface{}{
			"label": st, "kind": 14, "detail": "status", "documentation": doc,
		})
	}
	for pr, doc := range priorityDocs {
		items = append(items, map[string]interface{}{
			"label": pr, "kind": 14, "detail": "priority", "documentation": doc,
		})
	}
	items = append(items, map[string]interface{}{
		"label": "today", "kind": 14, "detail": "timestamp",
		"insertTextFormat": 2, "insertText": today(),
	})
	return items
}

var statusDocs = map[string]string{
	"open":       "The note is active and still needs attention.",
	"resolved":   "The issue or task has been dealt with; kept for the record.",
	"wontfix":    "Acknowledged but deliberately not planned for action.",
	"deprecated": "The note no longer applies to the current code.",
}

var priorityDocs = map[string]string{
	"P0": "Critical — drop everything.",
	"P1": "High — address in the current cycle.",
	"P2": "Medium — schedule soon.",
	"P3": "Low — backlog.",
}

// idCompletion offers every note ID in the workspace with proximity-ranked
// sortText tiers (§9.2).
func (s *server) idCompletion(uri string, d *doc, entries []NoteEntry, partial string, hashIdx uint, line, col uint, current *model.Note) []map[string]interface{} {
	sites, _ := s.refIndex(entries)
	dir := filepath.Dir(d.path)

	byID := map[string][]NoteEntry{}
	var order []string
	for _, e := range entries {
		id := e.N().ID
		if id == "" {
			continue
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = append(byID[id], e)
	}
	sort.Strings(order)

	var out []map[string]interface{}
	for _, id := range order {
		short := strings.TrimPrefix(id, "#")
		if partial != "" && !strings.HasPrefix(short, partial) {
			continue
		}
		alreadyRefd := false
		if current != nil {
			for _, r := range current.Refs {
				if r.Value == id {
					alreadyRefd = true
				}
			}
		}
		for _, def := range byID[id] {
			tier := "0003"
			if !alreadyRefd {
				for _, site := range sites[id] {
					if site.URI == uri && (current == nil || site.FromID != current.ID) {
						tier = "0000"
						break
					}
				}
				if tier == "0003" && def.URI == uri {
					tier = "0001"
				}
				if tier == "0003" && filepath.Dir(def.Loc.File) == dir {
					tier = "0002"
				}
			} else {
				tier = "9999"
			}
			start := hashIdx
			if start > col {
				start = col
			}
			item := map[string]interface{}{
				"label":      id,
				"kind":       18, // CompletionItemKind.Reference
				"detail":     completionDetail(def.N()),
				"filterText": short,
				"sortText":   tier,
				"documentation": map[string]interface{}{
					"kind":  "markdown",
					"value": shortNoteDoc(def, s.root),
				},
				"data": map[string]interface{}{
					"id":                   id,
					"uri":                  def.URI,
					"line":                 def.N().Range.StartLine,
					"resolveDocumentation": true,
				},
			}
			item["textEdit"] = map[string]interface{}{
				"range": map[string]interface{}{
					"start": map[string]interface{}{"line": line, "character": d.utf16Col(line, start)},
					"end":   map[string]interface{}{"line": line, "character": d.utf16Col(line, col)},
				},
				"newText": id,
			}
			if alreadyRefd {
				item["tags"] = []int{1} // CompletionItemTag.Deprecated
			}
			out = append(out, item)
		}
	}
	return out
}

// tagCompletion offers workspace tags with usage counts (§9.3).
func (s *server) tagCompletion(uri string, d *doc, entries []NoteEntry, e *NoteEntry, p string, line, col uint) []map[string]interface{} {
	counts, _ := tagCounts(entries)
	existing := map[string]bool{}
	if e != nil {
		for _, t := range e.N().Tags {
			existing[t] = true
		}
	}
	partial := ""
	tokStart := col
	if idx := strings.LastIndexByte(p, '#'); idx >= 0 {
		partial = p[idx+1:]
		tokStart = uint(idx)
	} else if idx := strings.LastIndexByte(p, ','); idx >= 0 {
		rest := p[idx+1:]
		tokStart = uint(idx + 1 + (len(rest) - len(strings.TrimLeft(rest, " \t"))))
	}

	type tagItem struct {
		item map[string]interface{}
		key  string
	}
	var items []tagItem
	for tag, cnt := range counts {
		if existing[tag] {
			continue
		}
		if partial != "" && !strings.HasPrefix(tag, partial) {
			continue
		}
		items = append(items, tagItem{
			key: fmt.Sprintf("%05d-%s", 100000-cnt, tag),
			item: map[string]interface{}{
				"label":      "#" + tag,
				"kind":       14,
				"detail":     fmt.Sprintf("used %d times", cnt),
				"filterText": tag,
				"sortText":   fmt.Sprintf("%04d", 10000-cnt),
				"textEdit": map[string]interface{}{
					"range": map[string]interface{}{
						"start": map[string]interface{}{"line": line, "character": d.utf16Col(line, tokStart)},
						"end":   map[string]interface{}{"line": line, "character": d.utf16Col(line, col)},
					},
					"newText": "#" + tag,
				},
			},
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	var out []map[string]interface{}
	for _, it := range items {
		out = append(out, it.item)
	}
	return out
}

// authorCompletion offers configured and indexed authors.
func (s *server) authorCompletion(entries []NoteEntry, partial string) []map[string]interface{} {
	seen := map[string]bool{}
	var authors []string
	for _, a := range s.cfg.KnownAuthors {
		if !seen[a] {
			seen[a] = true
			authors = append(authors, a)
		}
	}
	for _, e := range entries {
		for _, a := range e.N().Authors {
			if !seen[a] {
				seen[a] = true
				authors = append(authors, a)
			}
		}
	}
	sort.Strings(authors)
	var out []map[string]interface{}
	for _, a := range authors {
		if partial != "" && !strings.HasPrefix(a, partial) {
			continue
		}
		detail := "author"
		if contains(s.cfg.KnownAuthors, a) {
			detail = "known author"
		}
		out = addLSPItem(out, a, 6, detail, a)
	}
	return out
}

// snippetItems builds the §9.6 template snippets.
func (s *server) snippetItems(entries []NoteEntry) []map[string]interface{} {
	author := s.cfg.DefaultAuthor
	if author == "" {
		author = "your-name"
	}
	choice := []string{"observation", "todo", "issue", "context", "lesson", "prompt"}
	choice = append(choice, sortedCustomCategories(customCategoryCounts(entries))...)
	choices := strings.Join(choice, ",")

	item := func(label, templateID, insert, doc string) map[string]interface{} {
		return map[string]interface{}{
			"label":            label,
			"kind":             15, // CompletionItemKind.Snippet
			"detail":           "DevNotes template",
			"insertTextFormat": 2,
			"insertText":       insert,
			"documentation": map[string]interface{}{
				"kind":  "markdown",
				"value": doc,
			},
			"data": map[string]interface{}{"templateId": templateID},
		}
	}
	templates := []struct {
		label, id, insert, doc string
	}{
		{"@note — New DevNote", "note",
			"@note #${1:id} ${2|" + choices + "|} : ${3:title}\n@author ${4:" + author + "}\n\n${0:body}",
			"Minimal note header, author directive and body."},
		{"@note issue", "note-issue",
			"@note #${1:id} issue open P2 : ${2:title}\n@author ${3:" + author + "}\n@see ${4:#related}\n\n## Problem\n\n${5}\n\n## Recommendation\n\n${6}",
			"Full issue template with Problem / Recommendation body."},
		{"@note todo", "note-todo",
			"@note #${1:id} todo : ${2:title}\n@author ${3:" + author + "}\n\n${0:What needs to happen.}",
			"Task template."},
		{"@note context", "note-context",
			"@note #${1:id} context : ${2:title}\n@author ${3:" + author + "}\n\n${0:Why the code looks this way.}",
			"Design-context template."},
		{"@note lesson", "note-lesson",
			"@note #${1:id} lesson : ${2:title}\n@author ${3:" + author + "}\n\n${0:What was learned.}",
			"Lesson-learned template."},
		{"@note prompt", "note-prompt",
			"@note #${1:id} prompt : ${2:title}\n@author ${3:" + author + "}\n\n${0:Instructions for AI agents.}",
			"AI-agent prompt template."},
		{"@author", "author", "@author ${1:" + author + "}", "Author directive."},
		{"@see", "see", "@see #${1:id}", "Reference to another note (completion lists IDs after '#')."},
		{"@assign", "assign", "@assign ${1:" + author + "}", "Assign the note to someone."},
		{"@deadline", "deadline", "@deadline ${1:2026-12-31}", "Deadline extension directive (ISO date)."},
	}
	var out []map[string]interface{}
	for _, t := range templates {
		out = append(out, item(t.label, t.id, t.insert, t.doc))
	}
	return out
}

func (s *server) directiveItems() []map[string]interface{} {
	var out []map[string]interface{}
	out = addLSPItem(out, "@note", 14, "note header", "@note #id category : title")
	out = addLSPItem(out, "@author", 14, "directive", "@author ")
	out = addLSPItem(out, "@see", 14, "directive", "@see ")
	out = addLSPItem(out, "@assign", 14, "directive", "@assign ")
	out = addLSPItem(out, "@deadline", 14, "directive", "@deadline ")
	return out
}

// snippetsAllowed implements the §9.6 gating: devnotes files always get
// snippets; host files only when notes already exist or configuration
// enables all files.
func (s *server) snippetsAllowed(d *doc, entries []NoteEntry) bool {
	if s.cfg.SnippetsEnableAllFiles {
		return true
	}
	if d != nil && d.res != nil && len(d.res.NoteLocs) > 0 {
		return true
	}
	funcLang := func(ext string) string { return pipeline.Lang(ext) }
	if d != nil && funcLang(filepath.Ext(d.path)) == "dn" {
		return true
	}
	return false
}

// resolveCompletion implements completionItem/resolve: attach the full note
// preview or template documentation deferred from the initial response.
func (s *server) resolveCompletion(item map[string]interface{}) map[string]interface{} {
	data, _ := item["data"].(map[string]interface{})
	if data == nil {
		return item
	}
	if tpl, _ := data["templateId"].(string); tpl != "" {
		item["documentation"] = map[string]interface{}{
			"kind":  "markdown",
			"value": "Template `" + tpl + "`. Placeholders are jumped with Tab; `$0` is the final cursor position.",
		}
		return item
	}
	if resolveDoc, _ := data["resolveDocumentation"].(bool); resolveDoc {
		id, _ := data["id"].(string)
		turi, _ := data["uri"].(string)
		entries := s.allEntries()
		for _, def := range s.entriesWithID(entries, id) {
			if def.URI == turi {
				_, counts := s.refIndex(entries)
				item["documentation"] = map[string]interface{}{
					"kind":  "markdown",
					"value": renderNoteMarkdown(def, counts[id], s.cfg.HoverBodyExcerptChars, s.root),
				}
				break
			}
		}
	}
	return item
}

// ---------------------------------------------------------------------------
// Code actions
// ---------------------------------------------------------------------------

type lspDiagRef struct {
	diag map[string]interface{}
	code string
	raw  pipeline.Diag
}

func (s *server) codeActions(uri string, startL, startC, endL, endC uint, only []string) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil {
		return nil
	}
	startB := utf16ToByte(docLineAt(d, startL), startC)
	endB := utf16ToByte(docLineAt(d, endL), endC)
	sel := model.Range{StartLine: startL, StartCol: startB, EndLine: endL, EndCol: endB}
	lines := docLines(d)
	entries := s.allEntries()

	var out []map[string]interface{}
	editAction := func(title, kind string, edits []textEditB, diags ...map[string]interface{}) map[string]interface{} {
		if len(edits) == 0 {
			return nil
		}
		act := map[string]interface{}{
			"title": title,
			"kind":  kind,
			"edit":  map[string]interface{}{"changes": map[string]interface{}{uri: d.editsToLSP(edits)}},
		}
		if len(diags) > 0 {
			act["diagnostics"] = diags
		}
		return act
	}

	// 1. Quick fixes associated with diagnostics in the selection.
	for _, dg := range s.diagSet(d) {
		dr := model.Range{StartLine: dg.StartLine, StartCol: dg.StartCol, EndLine: dg.EndLine, EndCol: dg.EndCol}
		if !rangesIntersect(dr, sel) {
			continue
		}
		lsp := s.lspDiagFrom(d, dg)
		e := findEntryAt(d, dg.StartLine, dg.StartCol)
		var fix []textEditB
		var cmd map[string]interface{}
		switch dg.Code {
		case "MISSING_TITLE":
			if e != nil {
				if te, ok := titlePlaceholderEdit(d, e); ok {
					fix = []textEditB{te}
				}
			}
		case "INVALID_TIMESTAMP":
			fix = []textEditB{replaceTokenEdit(int(dg.StartLine), dr, today(), lines)}
		case "INVALID_PRIORITY", "DUPLICATE_FIELD":
			fix = []textEditB{replaceTokenEdit(int(dg.StartLine), dr, "", lines)}
		case "INVALID_TAG":
			if e != nil {
				if te, ok := removeTagEdit(d, e, dr); ok {
					fix = []textEditB{te}
				}
			}
		case "UNRESOLVED_REFERENCE":
			fix = []textEditB{removeLineEdit(int(dg.StartLine))}
		case "INVALID_ID":
			if e != nil && e.N().ID != "" {
				taken := map[string]bool{}
				for _, en := range entries {
					taken[en.N().ID] = true
				}
				fix = []textEditB{{StartLine: e.N().IDRange.StartLine, StartCol: e.N().IDRange.StartCol,
					EndLine: e.N().IDRange.EndLine, EndCol: e.N().IDRange.EndCol, NewText: suggestedID(e, taken)}}
			}
		case "DUPLICATE_ID":
			id, _ := dg.Data["id"].(string)
			cmd = map[string]interface{}{
				"title":       "Rename this note",
				"kind":        "quickfix",
				"command":     "devnotes.renameNote",
				"arguments":   []interface{}{map[string]interface{}{"uri": uri, "id": id}},
				"diagnostics": []interface{}{lsp},
			}
		case "UNKNOWN_CATEGORY":
			if e != nil {
				cat, _ := dg.Data["category"].(string)
				fix = []textEditB{insertDirectiveEdit(d, e, "@category "+cat)}
			}
		}
		if cmd != nil {
			out = append(out, cmd)
			continue
		}
		if act := editAction(quickFixTitle(dg), "quickfix", fix, lsp); act != nil {
			out = append(out, act)
		}
	}

	// 2-6. Structural actions on the note under the cursor.
	e := findEntryAt(d, startL, startB)
	if e != nil {
		n := e.N()
		current := n.Status
		if current == "" {
			current = "open"
		}
		// Status transitions.
		for _, target := range []string{"resolved", "wontfix", "deprecated", "open"} {
			if target == current {
				continue
			}
			title := "Mark as " + target
			if target == "open" {
				title = "Reopen"
			}
			title += " (currently: " + current + ")"
			if te, ok := statusEdit(d, e, target); ok {
				if act := editAction(title, "refactor.rewrite.devnotes.status", []textEditB{te}); act != nil {
					out = append(out, act)
				}
			}
		}
		// Priority changes.
		for _, p := range []string{"P0", "P1", "P2", "P3"} {
			if p == n.Priority {
				continue
			}
			if te, ok := priorityEdit(d, e, p); ok {
				if act := editAction("Set priority to "+p, "refactor.rewrite.devnotes.priority", []textEditB{te}); act != nil {
					out = append(out, act)
				}
			}
		}
		if n.HasPri {
			if te, ok := priorityEdit(d, e, ""); ok {
				if act := editAction("Remove priority (currently: "+n.Priority+")", "refactor.rewrite.devnotes.priority", []textEditB{te}); act != nil {
					out = append(out, act)
				}
			}
		}
		// Assignment.
		for _, author := range s.cfg.KnownAuthors {
			already := false
			for _, dir := range n.ExtDirs {
				if dir.Name == "assign" && strings.TrimSpace(dir.Value) == author {
					already = true
				}
			}
			if already {
				continue
			}
			te := assignEdit(d, e, author)
			if act := editAction("Assign to "+author, "refactor.rewrite.devnotes.assign", []textEditB{te}); act != nil {
				out = append(out, act)
			}
		}
		// Reference actions.
		te := insertDirectiveEdit(d, e, "@see ")
		if act := editAction("Add @see reference", "refactor.rewrite.devnotes.reference", []textEditB{te}); act != nil {
			out = append(out, act)
		}
		if ref := refAt(n, startL, startB); ref != nil {
			line := int(ref.Range.StartLine)
			if act := editAction("Remove @see "+ref.Value, "refactor.rewrite.devnotes.reference",
				[]textEditB{removeLineEdit(line)}); act != nil {
				out = append(out, act)
			}
		}
		// Format.
		if te := formatNoteEdits(d, e); len(te) > 0 {
			if act := editAction("Format note", "source.fixAll.devnotes", te); act != nil {
				out = append(out, act)
			}
		}
	}
	if act := s.formatFileAction(uri); act != nil {
		out = append(out, act)
	}

	return filterByOnly(out, only)
}

// quickFixTitle derives the action title per §8.5.
func quickFixTitle(dg pipeline.Diag) string {
	switch dg.Code {
	case "MISSING_TITLE":
		return "Insert placeholder title"
	case "INVALID_TIMESTAMP":
		return "Replace with today's date"
	case "INVALID_PRIORITY":
		return "Remove invalid priority"
	case "INVALID_TAG":
		return "Remove invalid tag"
	case "DUPLICATE_FIELD":
		return "Remove duplicate field"
	case "UNRESOLVED_REFERENCE":
		return "Remove dangling reference"
	case "INVALID_ID":
		return "Replace with suggested ID"
	case "UNKNOWN_CATEGORY":
		return "Promote to custom category"
	}
	return "Fix: " + dg.Code
}

func (s *server) formatFileAction(uri string) map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var all []textEditB
	for i := range d.res.NoteLocs {
		e := NoteEntry{Loc: d.res.NoteLocs[i], URI: uri}
		all = append(all, formatNoteEdits(d, &e)...)
	}
	if len(all) == 0 {
		return nil
	}
	return map[string]interface{}{
		"title": "Format all notes in file",
		"kind":  "source.fixAll.devnotes.file",
		"edit":  map[string]interface{}{"changes": map[string]interface{}{uri: d.editsToLSP(all)}},
	}
}

// filterByOnly applies the client's context.only filter (kind prefix match).
func filterByOnly(actions []map[string]interface{}, only []string) []map[string]interface{} {
	if len(only) == 0 {
		return actions
	}
	var out []map[string]interface{}
	for _, a := range actions {
		kind, _ := a["kind"].(string)
		for _, o := range only {
			if strings.HasPrefix(kind, o) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Rename
// ---------------------------------------------------------------------------

func (s *server) prepareRename(uri string, line, char uint) interface{} {
	d := s.docFor(uri)
	if d == nil {
		return nil
	}
	byteCol := utf16ToByte(docLineAt(d, line), char)
	e := findEntryAt(d, line, byteCol)
	if e == nil {
		return nil
	}
	n := e.N()
	var r model.Range
	var placeholder string
	if ref := refAt(n, line, byteCol); ref != nil && ref.Kind == "id" {
		r, placeholder = ref.Range, ref.Value
	} else if idAt(n, line, byteCol) {
		r, placeholder = n.IDRange, n.ID
	} else {
		return nil
	}
	return map[string]interface{}{"range": toLSPRange(d, r), "placeholder": placeholder}
}

// rename returns (WorkspaceEdit, errCode, errMsg).
func (s *server) rename(uri string, line, char uint, newName string) (interface{}, int, string) {
	d := s.docs[uri]
	if d == nil {
		return nil, errInvalidParams, "Unknown document"
	}
	newName = strings.TrimSpace(newName)
	if !strings.HasPrefix(newName, "#") {
		newName = "#" + newName
	}
	if !validRenameRe.MatchString(newName) {
		return nil, errInvalidParams, "Note IDs start with '#' and may only contain letters, digits, '-' and '_'."
	}
	byteCol := utf16ToByte(docLineAt(d, line), char)
	e := findEntryAt(d, line, byteCol)
	if e == nil {
		return nil, errInvalidParams, "Place the cursor on a note ID or an @see reference to rename."
	}
	n := e.N()
	var id string
	if ref := refAt(n, line, byteCol); ref != nil && ref.Kind == "id" {
		id = ref.Value
	} else if idAt(n, line, byteCol) {
		id = n.ID
	} else {
		return nil, errInvalidParams, "Place the cursor on a note ID or an @see reference to rename."
	}

	entries := s.allEntries()
	defs := s.entriesWithID(entries, id)
	if len(defs) > 1 {
		return nil, errInvalidRequest,
			fmt.Sprintf("Cannot rename %s: %d notes share this ID. Resolve the duplication first (see the DUPLICATE_ID diagnostics).", id, len(defs))
	}

	type fileEdits struct {
		uri     string
		path    string
		version interface{}
		edits   []textEditB
	}
	byFile := map[string]*fileEdits{}
	getFE := func(entry NoteEntry) *fileEdits {
		fe, ok := byFile[entry.Loc.File]
		if !ok {
			fe = &fileEdits{uri: entry.URI, path: entry.Loc.File}
			if dd, open := s.docs[entry.URI]; open {
				fe.version = dd.version
			}
			byFile[entry.Loc.File] = fe
		}
		return fe
	}
	for _, entry := range entries {
		fe := getFE(entry)
		en := entry.N()
		if en.ID == id {
			fe.edits = append(fe.edits, textEditB{
				StartLine: en.IDRange.StartLine, StartCol: en.IDRange.StartCol,
				EndLine: en.IDRange.EndLine, EndCol: en.IDRange.EndCol, NewText: newName,
			})
		}
		for _, ref := range en.Refs {
			if ref.Kind == "id" && ref.Value == id {
				fe.edits = append(fe.edits, textEditB{
					StartLine: ref.Range.StartLine, StartCol: ref.Range.StartCol,
					EndLine: ref.Range.EndLine, EndCol: ref.Range.EndCol, NewText: newName,
				})
			}
		}
	}
	if len(byFile) == 0 {
		return nil, errInvalidParams, "Nothing to rename."
	}

	var changes []interface{}
	for _, fe := range byFile {
		var lspEdits []map[string]interface{}
		if dd, open := s.docs[fe.uri]; open {
			lspEdits = dd.editsToLSP(fe.edits)
		} else {
			lspEdits = editsToLSPForFile(fe.path, fe.edits)
		}
		changes = append(changes, map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": fe.uri, "version": fe.version},
			"edits":        lspEdits,
		})
	}
	return map[string]interface{}{"documentChanges": changes}, 0, ""
}

// editsToLSPForFile converts edits for an unopened file by reading it.
func editsToLSPForFile(path string, edits []textEditB) []map[string]interface{} {
	src := readWorkspaceFile(path)
	var lines [][]byte
	start := 0
	for i, c := range src {
		if c == '\n' {
			lines = append(lines, src[start:i])
			start = i + 1
		}
	}
	if start < len(src) {
		lines = append(lines, src[start:])
	}
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

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func (s *server) formatting(uri string, rng *model.Range) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var all []textEditB
	for i := range d.res.NoteLocs {
		e := NoteEntry{Loc: d.res.NoteLocs[i], URI: uri}
		if rng != nil && !rangesIntersect(e.N().Range, *rng) {
			continue
		}
		all = append(all, formatNoteEdits(d, &e)...)
	}
	return d.editsToLSP(all)
}
