// textDocument handlers: hover, definition, references, symbols, document
// links, folding/selection ranges, inlay hints, code lens and pull
// diagnostics.
package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

// docFor returns the open document for a URI, or lazily parses the file on
// disk (spec §18: requests against unindexed files trigger an on-demand
// parse). The temporary doc is not added to s.docs.
func (s *server) docFor(uri string) *doc {
	if d, ok := s.docs[uri]; ok {
		return d
	}
	path := uriToPath(uri)
	src := readWorkspaceFile(path)
	if src == nil || pipeline.Lang(filepath.Ext(path)) == "" {
		return nil
	}
	res, err := pipeline.CheckFile(path, filepath.Ext(path), src)
	if err != nil {
		return nil
	}
	return &doc{uri: uri, path: path, text: src, version: -1, res: &res}
}

// findEntryAt returns the note containing the (line, byteCol) position.
func findEntryAt(d *doc, line, col uint) *NoteEntry {
	if d == nil || d.res == nil {
		return nil
	}
	for i := range d.res.NoteLocs {
		if rngContains(d.res.NoteLocs[i].Note.Range, line, col) {
			return &NoteEntry{Loc: d.res.NoteLocs[i], URI: d.uri}
		}
	}
	return nil
}

// refAt returns the @see reference token containing the position.
func refAt(n model.Note, line, col uint) *model.Reference {
	for i := range n.Refs {
		if rngContains(n.Refs[i].Range, line, col) {
			return &n.Refs[i]
		}
	}
	return nil
}

// idAt reports whether the position is on the note's header ID token.
func idAt(n model.Note, line, col uint) bool {
	if n.IDRange.StartLine == 0 && n.IDRange.StartCol == 0 && n.IDRange.EndLine == 0 && n.IDRange.EndCol == 0 {
		return false
	}
	return rngContains(n.IDRange, line, col)
}

// toLSPRange converts a byte-coordinate range to an LSP range for the doc.
func toLSPRange(d *doc, r model.Range) map[string]interface{} {
	lines := docLines(d)
	sc, ec := r.StartCol, r.EndCol
	if int(r.StartLine) < len(lines) {
		sc = pipeline.UTF16Col(lines[r.StartLine], r.StartCol)
	}
	if int(r.EndLine) < len(lines) {
		ec = pipeline.UTF16Col(lines[r.EndLine], r.EndCol)
	}
	return map[string]interface{}{
		"start": map[string]interface{}{"line": r.StartLine, "character": sc},
		"end":   map[string]interface{}{"line": r.EndLine, "character": ec},
	}
}

// ---------------------------------------------------------------------------
// Diagnostics (shared between push and pull)
// ---------------------------------------------------------------------------

// wsByPath computes workspace-wide checks (duplicate IDs, unresolved
// references) over the merged index once, keyed by file path.
func (s *server) wsByPath() map[string][]validate.WSDiag {
	var locs []validate.NoteLoc
	for _, e := range s.allEntries() {
		locs = append(locs, e.Loc)
	}
	out := map[string][]validate.WSDiag{}
	for _, dg := range validate.CheckWorkspace(locs) {
		out[dg.File] = append(out[dg.File], dg)
	}
	return out
}

// diagSet computes the full diagnostic set for one document: per-file
// diagnostics plus workspace checks attributed to this file.
func (s *server) diagSet(d *doc) []pipeline.Diag {
	if d.res == nil {
		return nil
	}
	var out []pipeline.Diag
	out = append(out, d.res.Diags...)
	for _, dg := range s.wsByPath()[d.path] {
		out = append(out, pipeline.Diag{
			File: d.path, Severity: dg.Severity, Code: dg.Code, Message: dg.Message,
			StartLine: dg.Range.StartLine, StartCol: dg.Range.StartCol,
			EndLine: dg.Range.EndLine, EndCol: dg.Range.EndCol,
			Related: dg.Related, Data: dg.Data,
		})
	}
	return out
}

// lspDiagFrom converts a pipeline.Diag into the LSP wire shape, adding
// relatedInformation and structured data.
func (s *server) lspDiagFrom(d *doc, dg pipeline.Diag) map[string]interface{} {
	lines := docLines(d)
	sl, sc := dg.StartLine, dg.StartCol
	el, ec := dg.EndLine, dg.EndCol
	if posLess(el, ec, sl, sc) {
		sl, sc, el, ec = el, ec, sl, sc
	}
	if int(sl) < len(lines) {
		sc = pipeline.UTF16Col(lines[sl], sc)
	}
	if int(el) < len(lines) {
		ec = pipeline.UTF16Col(lines[el], ec)
	}
	diag := map[string]interface{}{
		"range": map[string]interface{}{
			"start": map[string]interface{}{"line": sl, "character": sc},
			"end":   map[string]interface{}{"line": el, "character": ec},
		},
		"severity": int(dg.Severity) + 1, // LSP: 1=Error 2=Warning 3=Info 4=Hint
		"code":     dg.Code,
		"source":   serverName,
		"message":  strings.TrimSpace(dg.Message),
	}
	if len(dg.Data) > 0 {
		diag["data"] = dg.Data
	}
	var related []map[string]interface{}
	for _, r := range dg.Related {
		uri := d.uri
		if r.File != "" {
			uri = pathToURI(r.File)
		}
		related = append(related, map[string]interface{}{
			"location": map[string]interface{}{
				"uri":   uri,
				"range": map[string]interface{}{"start": map[string]interface{}{"line": r.Range.StartLine, "character": r.Range.StartCol}, "end": map[string]interface{}{"line": r.Range.EndLine, "character": r.Range.EndCol}},
			},
			"message": r.Message,
		})
	}
	if related != nil {
		diag["relatedInformation"] = related
	}
	return diag
}

// ---------------------------------------------------------------------------
// Hover
// ---------------------------------------------------------------------------

func (s *server) hover(uri string, line, char uint) map[string]interface{} {
	d := s.docFor(uri)
	if d == nil {
		return nil
	}
	byteCol := utf16ToByte(docLineAt(d, line), char)
	e := findEntryAt(d, line, byteCol)
	if e == nil {
		return nil
	}

	entries := s.allEntries()
	_, counts := s.refIndex(entries)

	if ref := refAt(e.N(), line, byteCol); ref != nil {
		md := renderReferenceMarkdown(*e, *ref, entries, counts[ref.Value], s.cfg.HoverBodyExcerptChars, s.root)
		return map[string]interface{}{
			"contents": map[string]interface{}{"kind": "markdown", "value": md},
			"range":    toLSPRange(d, ref.Range),
		}
	}

	md := renderNoteMarkdown(*e, counts[e.N().ID], s.cfg.HoverBodyExcerptChars, s.root)
	return map[string]interface{}{
		"contents": map[string]interface{}{"kind": "markdown", "value": md},
		"range":    toLSPRange(d, e.N().Range),
	}
}

// ---------------------------------------------------------------------------
// Definition
// ---------------------------------------------------------------------------

func (s *server) definition(uri string, line, char uint) interface{} {
	d := s.docFor(uri)
	if d == nil {
		return nil
	}
	byteCol := utf16ToByte(docLineAt(d, line), char)
	e := findEntryAt(d, line, byteCol)
	if e == nil {
		return nil
	}
	entries := s.allEntries()

	var targetRef *model.Reference
	var origin model.Range
	if ref := refAt(e.N(), line, byteCol); ref != nil && ref.Kind == "id" {
		targetRef = ref
		origin = ref.Range
	} else if idAt(e.N(), line, byteCol) {
		// Definition on a header ID: point at itself.
		origin = e.N().IDRange
		return []interface{}{s.locationLink(d, origin, *e, e.N().IDRange)}
	}
	if targetRef == nil {
		return nil
	}
	defs := s.entriesWithID(entries, targetRef.Value)
	var out []interface{}
	for i := range defs {
		out = append(out, s.locationLink(d, origin, defs[i], defs[i].N().IDRange))
	}
	return out
}

// locationLink builds a LocationLink from an origin token range to the
// target note.
func (s *server) locationLink(d *doc, origin model.Range, target NoteEntry, selection model.Range) map[string]interface{} {
	return map[string]interface{}{
		"originSelectionRange": toLSPRange(d, origin),
		"targetUri":            target.URI,
		"targetRange":          s.rangeFor(target),
		"targetSelectionRange": s.rangeForAt(target, selection),
	}
}

// rangeFor converts a note range to LSP coordinates in its own document.
func (s *server) rangeFor(e NoteEntry) map[string]interface{} {
	return s.rangeForAt(e, e.N().Range)
}

func (s *server) rangeForAt(e NoteEntry, r model.Range) map[string]interface{} {
	if d, ok := s.docs[e.URI]; ok {
		return toLSPRange(d, r)
	}
	// Unopened file: reconstruct line lengths from the indexed text.
	return map[string]interface{}{
		"start": map[string]interface{}{"line": r.StartLine, "character": r.StartCol},
		"end":   map[string]interface{}{"line": r.EndLine, "character": r.EndCol},
	}
}

// ---------------------------------------------------------------------------
// References
// ---------------------------------------------------------------------------

func (s *server) references(uri string, line, char uint, includeDecl bool) []map[string]interface{} {
	d := s.docFor(uri)
	if d == nil {
		return nil
	}
	byteCol := utf16ToByte(docLineAt(d, line), char)
	e := findEntryAt(d, line, byteCol)
	if e == nil {
		return nil
	}
	var id string
	if ref := refAt(e.N(), line, byteCol); ref != nil && ref.Kind == "id" {
		id = ref.Value
	} else if idAt(e.N(), line, byteCol) {
		id = e.N().ID
	} else {
		return nil
	}

	entries := s.allEntries()
	sites, _ := s.refIndex(entries)
	var out []map[string]interface{}
	for _, site := range sites[id] {
		out = append(out, map[string]interface{}{
			"uri":   site.URI,
			"range": s.rangeForAt(NoteEntry{Loc: validate.NoteLoc{File: site.Path, Note: noteWithRange(site)}, URI: site.URI}, site.Range),
		})
	}
	if includeDecl {
		for _, def := range s.entriesWithID(entries, id) {
			out = append(out, map[string]interface{}{
				"uri":   def.URI,
				"range": s.rangeForAt(def, def.N().IDRange),
			})
		}
	}
	return out
}

// noteWithRange wraps a bare range in a minimal note for range conversion.
func noteWithRange(site refSite) model.Note {
	return model.Note{Range: site.Range}
}

// ---------------------------------------------------------------------------
// Symbols
// ---------------------------------------------------------------------------

func (s *server) documentSymbols(uri string) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, loc := range d.res.NoteLocs {
		n := loc.Note
		out = append(out, map[string]interface{}{
			"name":           n.ID + " — " + n.Title,
			"detail":         containerName(n),
			"kind":           5, // SymbolKind.Object
			"range":          toLSPRange(d, n.Range),
			"selectionRange": toLSPRange(d, n.IDRange),
			"children":       []interface{}{},
		})
	}
	return out
}

func (s *server) workspaceSymbols(query string) []map[string]interface{} {
	entries := s.allEntries()
	q := strings.ToLower(strings.TrimPrefix(query, "#"))
	var out []map[string]interface{}
	for _, e := range entries {
		n := e.N()
		if q != "" && !strings.Contains(strings.ToLower(strings.TrimPrefix(n.ID, "#")), q) {
			continue
		}
		out = append(out, map[string]interface{}{
			"name": n.ID,
			"kind": 5,
			"location": map[string]interface{}{
				"uri":   e.URI,
				"range": s.rangeFor(e),
			},
			"containerName": containerName(n),
		})
		if len(out) >= 500 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Document links
// ---------------------------------------------------------------------------

func (s *server) documentLinks(uri string) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	entries := s.allEntries()
	var out []map[string]interface{}
	for _, loc := range d.res.NoteLocs {
		for _, ref := range loc.Note.Refs {
			link := map[string]interface{}{"range": toLSPRange(d, ref.Range)}
			switch ref.Kind {
			case "url":
				link["target"] = ref.Value
				link["tooltip"] = ref.Value
			case "id":
				defs := s.entriesWithID(entries, ref.Value)
				if len(defs) == 0 {
					link["tooltip"] = "Unresolved reference: " + ref.Value
				} else {
					tgt := defs[0]
					link["target"] = tgt.URI + "#L" + itoa(int(tgt.N().IDRange.StartLine)+1)
					tip := tgt.N().Title
					if tip == "" {
						tip = "(untitled)"
					}
					if len(defs) > 1 {
						tip = "Ambiguous: " + itoa(len(defs)) + " notes share this ID — " + tip
					}
					link["tooltip"] = tip
				}
			default:
				link["tooltip"] = ref.Value
			}
			out = append(out, link)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Folding + selection ranges
// ---------------------------------------------------------------------------

func (s *server) foldingRanges(uri string) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var out []map[string]interface{}
	for i := range d.res.NoteLocs {
		e := NoteEntry{Loc: d.res.NoteLocs[i], URI: uri}
		_, sep, bodyStart, bodyEnd := noteMetaBody(d, &e)
		if sep < 0 || bodyStart < 0 || bodyEnd < bodyStart {
			continue
		}
		out = append(out, map[string]interface{}{
			"startLine": bodyStart,
			"endLine":   bodyEnd,
			"kind":      "region",
		})
	}
	return out
}

// lspPos is one client position in a selectionRange request.
type lspPos struct {
	Line      uint `json:"line"`
	Character uint `json:"character"`
}

func (s *server) selectionRanges(uri string, positions []lspPos) []interface{} {
	d := s.docs[uri]
	if d == nil {
		return nil
	}
	var out []interface{}
	for _, p := range positions {
		byteCol := utf16ToByte(docLineAt(d, p.Line), p.Character)
		e := findEntryAt(d, p.Line, byteCol)
		if e == nil {
			out = append(out, nil)
			continue
		}
		n := e.N()
		// Chain: token -> header/directive line -> note.
		var chain []interface{}
		token := tokenRangeAt(n, p.Line, byteCol)
		if token != nil {
			chain = append(chain, toLSPRange(d, *token))
		}
		if hr := lineRangeFor(n, p.Line); hr != nil {
			chain = append(chain, toLSPRange(d, *hr))
		}
		chain = append(chain, toLSPRange(d, n.Range))
		out = append(out, map[string]interface{}{"range": chain[0], "parent": nestParents(chain)})
	}
	return out
}

func nestParents(chain []interface{}) interface{} {
	if len(chain) < 2 {
		return nil
	}
	return map[string]interface{}{"range": chain[1], "parent": nestParents(chain[1:])}
}

// tokenRangeAt finds the smallest recorded token range containing pos.
func tokenRangeAt(n model.Note, line, col uint) *model.Range {
	var best *model.Range
	consider := func(r model.Range) {
		if rngContains(r, line, col) {
			if best == nil || spanLess(r, *best) {
				v := r
				best = &v
			}
		}
	}
	consider(n.IDRange)
	consider(n.CategoryRange)
	consider(n.TitleRange)
	for _, f := range n.Fields {
		consider(f.Range)
	}
	for _, r := range n.Refs {
		consider(r.Range)
	}
	for _, r := range n.TagRanges {
		consider(r)
	}
	for _, r := range n.AuthorLocs {
		consider(r)
	}
	for _, dr := range n.ExtDirs {
		consider(dr.Range)
	}
	return best
}

func spanLess(a, b model.Range) bool {
	spanA := (a.EndLine-a.StartLine)*10000 + (a.EndCol - a.StartCol)
	spanB := (b.EndLine-b.StartLine)*10000 + (b.EndCol - b.StartCol)
	return spanA < spanB
}

// lineRangeFor returns the header or directive line range containing line.
func lineRangeFor(n model.Note, line uint) *model.Range {
	if rngContains(n.HeaderRange, line, 0) || (line == n.HeaderRange.StartLine) {
		v := n.HeaderRange
		return &v
	}
	for _, dr := range n.DirectiveRanges {
		if line >= dr.StartLine && line <= dr.EndLine {
			v := dr
			return &v
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Inlay hints + code lens (optional §16 capabilities)
// ---------------------------------------------------------------------------

func (s *server) inlayHints(uri string, startL, endL uint) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	entries := s.allEntries()
	_, counts := s.refIndex(entries)
	lines := docLines(d)

	var out []map[string]interface{}
	for _, loc := range d.res.NoteLocs {
		n := loc.Note
		hl := int(n.HeaderRange.StartLine)
		if hl < int(startL) || hl > int(endL) || hl >= len(lines) {
			continue
		}
		var parts []string
		st := n.Status
		if st == "" {
			st = "open"
		}
		parts = append(parts, st)
		if n.Priority != "" {
			parts = append(parts, n.Priority)
		}
		if c := counts[n.ID]; c > 0 {
			parts = append(parts, itoa(c)+" ref"+plural(c))
		}
		label := "· " + strings.Join(parts, " · ")
		col := pipeline.UTF16Col(lines[hl], uint(len(lines[hl])))
		out = append(out, map[string]interface{}{
			"position":    map[string]interface{}{"line": uint(hl), "character": col},
			"label":       label,
			"paddingLeft": true,
		})
		for _, ref := range n.Refs {
			if ref.Kind != "id" {
				continue
			}
			rl := int(ref.Range.EndLine)
			if rl < int(startL) || rl > int(endL) || rl >= len(lines) {
				continue
			}
			var lbl string
			if c := counts[ref.Value]; c > 0 || len(s.entriesWithID(entries, ref.Value)) > 0 {
				lbl = "↗ " + itoa(c)
			} else {
				lbl = "unresolved"
			}
			col := pipeline.UTF16Col(lines[rl], ref.Range.EndCol)
			out = append(out, map[string]interface{}{
				"position":     map[string]interface{}{"line": uint(rl), "character": col},
				"label":        lbl,
				"paddingLeft":  true,
				"paddingRight": true,
			})
		}
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (s *server) codeLens(uri string) []map[string]interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, loc := range d.res.NoteLocs {
		n := loc.Note
		r := model.Range{StartLine: n.Range.StartLine, StartCol: n.Range.StartCol,
			EndLine: n.Range.StartLine, EndCol: n.Range.StartCol}
		data, _ := json.Marshal(map[string]interface{}{
			"uri": uri, "id": n.ID,
			"sl": r.StartLine, "sc": r.StartCol, "el": r.EndLine, "ec": r.EndCol,
		})
		out = append(out, map[string]interface{}{
			"range": toLSPRange(d, r),
			"data":  json.RawMessage(data),
		})
	}
	return out
}

// resolveCodeLens fills the lens command from its data payload.
func (s *server) resolveCodeLens(raw json.RawMessage) map[string]interface{} {
	var payload struct {
		URI            string `json:"uri"`
		ID             string `json:"id"`
		SL, SC, EL, EC uint
	}
	_ = json.Unmarshal(raw, &payload)
	entries := s.allEntries()
	_, counts := s.refIndex(entries)
	title := payload.ID + " — " + itoa(counts[payload.ID]) + " reference" + plural(counts[payload.ID])
	if defs := s.entriesWithID(entries, payload.ID); len(defs) > 0 {
		title = payload.ID + " · " + containerName(defs[0].N()) + " · " + itoa(counts[payload.ID]) + " reference" + plural(counts[payload.ID])
	}
	r := model.Range{StartLine: payload.SL, StartCol: payload.SC, EndLine: payload.EL, EndCol: payload.EC}
	var rng interface{} = map[string]interface{}{
		"start": map[string]interface{}{"line": r.StartLine, "character": r.StartCol},
		"end":   map[string]interface{}{"line": r.EndLine, "character": r.EndCol},
	}
	if d, ok := s.docs[payload.URI]; ok {
		rng = toLSPRange(d, r)
	}
	return map[string]interface{}{
		"range": rng,
		"data":  raw,
		"command": map[string]interface{}{
			"title":     title,
			"command":   "devnotes.get",
			"arguments": []interface{}{map[string]interface{}{"id": payload.ID}},
		},
	}
}

// docLineAt returns the raw bytes of a doc line.
func docLineAt(d *doc, line uint) []byte {
	lines := docLines(d)
	if int(line) < len(lines) {
		return lines[line]
	}
	return nil
}
