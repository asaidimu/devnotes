// Custom workspace commands (devnotes.list/get/graph/nextInFile/prevInFile/
// exportContext/reindex/renameNote) and the resolve endpoints.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
)

// listItem renders one DevNoteListItem (§6.2).
func (s *server) listItem(e NoteEntry, refCounts map[string]int, includeBody bool) map[string]interface{} {
	n := e.N()
	tags := make([]string, 0, len(n.Tags))
	for _, t := range n.Tags {
		tags = append(tags, "#"+t)
	}
	var authors []string
	authors = append(authors, n.Authors...)

	secondary := containerName(n)
	if len(tags) > 0 {
		secondary += " · " + strings.Join(tags, ",")
	}
	item := map[string]interface{}{
		"id":             n.ID,
		"category":       n.Category,
		"status":         statusOrOpen(n),
		"priority":       model.PriorityLabel(n.Priority),
		"timestamp":      n.Timestamp,
		"tags":           tags,
		"title":          n.Title,
		"authors":        authors,
		"referenceCount": refCounts[n.ID],
		"location": map[string]interface{}{
			"uri":   e.URI,
			"range": s.rangeFor(e),
		},
		"display": map[string]interface{}{
			"primary":   titleOr(n),
			"secondary": secondary,
			"tertiary":  relPath(s.root, e.Loc.File) + ":" + itoa(int(n.Range.StartLine)+1),
		},
	}
	if includeBody {
		item["body"] = n.Body
	}
	return item
}

func statusOrOpen(n model.Note) string {
	if n.Status == "" {
		return "open"
	}
	return n.Status
}

func titleOr(n model.Note) string {
	if n.Title == "" {
		return "(untitled)"
	}
	return n.Title
}

// decodeListArgs parses the devnotes.list arguments object; string-or-array
// filter fields are accepted in both shapes.
func decodeListArgs(raw json.RawMessage) (listArgs, error) {
	var args listArgs
	if len(raw) == 0 {
		return args, nil
	}
	var obj struct {
		Filter      map[string]json.RawMessage `json:"filter"`
		Sort        string                     `json:"sort"`
		Limit       int                        `json:"limit"`
		IncludeBody bool                       `json:"includeBody"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return args, fmt.Errorf("malformed filter object: filter must be an object")
	}
	args.Sort = obj.Sort
	args.Limit = obj.Limit
	args.IncludeBody = obj.IncludeBody

	strOrList := func(m map[string]json.RawMessage, key string) ([]string, bool, error) {
		v, ok := m[key]
		if !ok {
			return nil, false, nil
		}
		var single string
		if err := json.Unmarshal(v, &single); err == nil {
			return []string{single}, true, nil
		}
		var list []string
		if err := json.Unmarshal(v, &list); err == nil {
			return list, true, nil
		}
		return nil, false, fmt.Errorf("filter.%s must be a string or string array", key)
	}
	var err error
	if args.Filter.Category, _, err = strOrList(obj.Filter, "category"); err != nil {
		return args, err
	}
	if args.Filter.Status, _, err = strOrList(obj.Filter, "status"); err != nil {
		return args, err
	}
	if args.Filter.Priority, _, err = strOrList(obj.Filter, "priority"); err != nil {
		return args, err
	}
	if args.Filter.Tag, _, err = strOrList(obj.Filter, "tag"); err != nil {
		return args, err
	}
	if v, ok := obj.Filter["author"]; ok {
		if err := json.Unmarshal(v, &args.Filter.Author); err != nil {
			return args, fmt.Errorf("filter.author must be a string")
		}
	}
	if v, ok := obj.Filter["file"]; ok {
		if err := json.Unmarshal(v, &args.Filter.File); err != nil {
			return args, fmt.Errorf("filter.file must be a string")
		}
	}
	if v, ok := obj.Filter["text"]; ok {
		if err := json.Unmarshal(v, &args.Filter.Text); err != nil {
			return args, fmt.Errorf("filter.text must be a string")
		}
	}
	if v, ok := obj.Filter["reference"]; ok {
		if err := json.Unmarshal(v, &args.Filter.Reference); err != nil {
			return args, fmt.Errorf("filter.reference must be a string")
		}
	}
	if v, ok := obj.Filter["includeResolved"]; ok {
		_ = json.Unmarshal(v, &args.Filter.IncludeResolved)
	}
	if v, ok := obj.Filter["includeDeprecated"]; ok {
		_ = json.Unmarshal(v, &args.Filter.IncludeDeprecated)
	}
	return args, nil
}

// listNotes implements devnotes.list.
func (s *server) listNotes(raw json.RawMessage) (interface{}, int, string) {
	args, err := decodeListArgs(raw)
	if err != nil {
		return nil, errInvalidParams, err.Error()
	}
	limit := args.Limit
	if limit <= 0 {
		limit = s.cfg.ListDefaultLimit
	}
	entries := s.allEntries()
	var matched []NoteEntry
	for _, e := range entries {
		if matchEntry(e, args.Filter, s.root, args.IncludeBody) {
			matched = append(matched, e)
		}
	}
	total := len(matched)
	sortEntries(matched, args.Sort)
	truncated := false
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
		truncated = true
	}
	_, counts := s.refIndex(entries)
	items := make([]map[string]interface{}, 0, len(matched))
	for _, e := range matched {
		items = append(items, s.listItem(e, counts, args.IncludeBody))
	}
	return map[string]interface{}{
		"items":     items,
		"total":     total,
		"truncated": truncated,
	}, 0, ""
}

// atPosition implements devnotes.atPosition: the note whose block contains a
// source line, used by statuslines and other position-aware UI. Returns nil
// when the cursor is not inside a note.
func (s *server) atPosition(raw json.RawMessage) (interface{}, int, string) {
	var args struct {
		URI  string `json:"uri"`
		Line int    `json:"line"`
	}
	if len(raw) > 0 {
		var wrapper []json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) > 0 {
			raw = wrapper[0]
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errInvalidParams, "arguments must contain {\"uri\", \"line\"}"
		}
	}
	if args.URI == "" || args.Line < 0 {
		return nil, errInvalidParams, "arguments must contain {\"uri\", \"line\"}"
	}
	d := s.docs[args.URI]
	if d == nil || d.res == nil {
		return nil, 0, ""
	}
	line := uint(args.Line)
	for _, loc := range d.res.NoteLocs {
		r := loc.Note.Range
		if line < r.StartLine || line > r.EndLine {
			continue
		}
		entries := s.allEntries()
		_, counts := s.refIndex(entries)
		e := NoteEntry{Loc: loc, URI: args.URI}
		item := s.listItem(e, counts, false)
		item["detail"] = containerName(loc.Note)
		return item, 0, ""
	}
	return nil, 0, ""
}

// getNote implements devnotes.get.
func (s *server) getNote(raw json.RawMessage) (interface{}, int, string) {
	var args struct {
		ID string `json:"id"`
	}
	if len(raw) > 0 {
		var wrapper []json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) > 0 {
			raw = wrapper[0]
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errInvalidParams, "arguments must contain {\"id\": \"#...\"}"
		}
	}
	if args.ID == "" {
		return nil, errInvalidParams, "arguments must contain {\"id\": \"#...\"}"
	}
	entries := s.allEntries()
	_, counts := s.refIndex(entries)
	defs := s.entriesWithID(entries, args.ID)
	switch {
	case len(defs) == 0:
		return nil, 0, ""
	case len(defs) == 1:
		return s.listItem(defs[0], counts, true), 0, ""
	default:
		out := make([]interface{}, 0, len(defs))
		for _, d := range defs {
			out = append(out, s.listItem(d, counts, true))
		}
		return out, 0, ""
	}
}

// graph implements devnotes.graph with cycle-safe BFS.
func (s *server) graph(raw json.RawMessage) (interface{}, int, string) {
	var args struct {
		Root      string `json:"root"`
		Depth     int    `json:"depth"`
		Direction string `json:"direction"`
	}
	if len(raw) > 0 {
		var wrapper []json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) > 0 {
			raw = wrapper[0]
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errInvalidParams, "arguments must contain {\"root\": \"#...\"}"
		}
	}
	if args.Root == "" {
		return nil, errInvalidParams, "arguments must contain {\"root\": \"#...\"}"
	}
	if args.Direction == "" {
		args.Direction = "both"
	}
	if args.Direction != "out" && args.Direction != "in" && args.Direction != "both" {
		return nil, errInvalidParams, "direction must be one of \"out\", \"in\", \"both\""
	}
	if args.Depth <= 0 {
		args.Depth = 100 // effectively unlimited, but bounded
	}

	entries := s.allEntries()
	sites, _ := s.refIndex(entries)
	byID := map[string][]NoteEntry{}
	for _, e := range entries {
		if e.N().ID != "" {
			byID[e.N().ID] = append(byID[e.N().ID], e)
		}
	}

	type edge struct{ from, to string }
	seenNode := map[string]bool{}
	seenEdge := map[string]bool{}
	var nodes []interface{}
	var edges []interface{}

	// BFS over both directions.
	type qItem struct {
		id    string
		depth int
	}
	queue := []qItem{{strings.TrimPrefix(args.Root, "#"), 0}}
	queue[0].id = args.Root
	if !strings.HasPrefix(queue[0].id, "#") {
		queue[0].id = "#" + queue[0].id
	}
	seenNode[queue[0].id] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if defs, ok := byID[cur.id]; ok && !seenNode["n:"+cur.id] {
			seenNode["n:"+cur.id] = true
			n := defs[0].N()
			nodes = append(nodes, map[string]interface{}{
				"id":       n.ID,
				"category": n.Category,
				"title":    n.Title,
				"location": map[string]interface{}{
					"uri":   defs[0].URI,
					"range": s.rangeFor(defs[0]),
				},
			})
		}
		if cur.depth >= args.Depth {
			continue
		}
		// Outgoing edges: cur -> its @see targets.
		if args.Direction == "out" || args.Direction == "both" {
			for _, e := range byID[cur.id] {
				for _, ref := range e.N().Refs {
					if ref.Kind != "id" || ref.Value == "" {
						continue
					}
					ek := cur.id + "->" + ref.Value
					if !seenEdge[ek] {
						seenEdge[ek] = true
						edges = append(edges, map[string]string{"from": cur.id, "to": ref.Value})
					}
					if !seenNode[ref.Value] {
						seenNode[ref.Value] = true
						queue = append(queue, qItem{ref.Value, cur.depth + 1})
					}
				}
			}
		}
		// Incoming edges: sites referencing cur.
		if args.Direction == "in" || args.Direction == "both" {
			for _, site := range sites[cur.id] {
				ek := site.FromID + "->" + cur.id
				if !seenEdge[ek] {
					seenEdge[ek] = true
					edges = append(edges, map[string]string{"from": site.FromID, "to": cur.id})
				}
				if !seenNode[site.FromID] {
					seenNode[site.FromID] = true
					queue = append(queue, qItem{site.FromID, cur.depth + 1})
				}
			}
		}
	}
	if nodes == nil {
		nodes = []interface{}{}
	}
	if edges == nil {
		edges = []interface{}{}
	}
	return map[string]interface{}{"nodes": nodes, "edges": edges}, 0, ""
}

// nextPrevInFile implements devnotes.nextInFile / devnotes.prevInFile.
func (s *server) nextPrevInFile(uri string, line int, wrap, next bool) interface{} {
	d := s.docs[uri]
	if d == nil || d.res == nil {
		return nil
	}
	var lines []int
	for _, loc := range d.res.NoteLocs {
		lines = append(lines, int(loc.Note.Range.StartLine))
	}
	sort.Ints(lines)
	if len(lines) == 0 {
		return nil
	}
	var target int
	found := false
	if next {
		for _, l := range lines {
			if l > line {
				target = l
				found = true
				break
			}
		}
		if !found && wrap {
			target = lines[0]
			found = true
		}
	} else {
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i] < line {
				target = lines[i]
				found = true
				break
			}
		}
		if !found && wrap {
			target = lines[len(lines)-1]
			found = true
		}
	}
	if !found {
		return nil
	}
	for _, loc := range d.res.NoteLocs {
		if int(loc.Note.Range.StartLine) == target {
			return map[string]interface{}{
				"uri":   uri,
				"range": toLSPRange(d, loc.Note.Range),
			}
		}
	}
	return nil
}

// exportContext implements devnotes.exportContext: a Markdown (or JSON)
// rendering of the filtered notes for AI context windows.
func (s *server) exportContext(raw json.RawMessage) (interface{}, int, string) {
	args, err := decodeListArgs(raw)
	if err != nil {
		return nil, errInvalidParams, err.Error()
	}
	format := "markdown"
	if len(raw) > 0 {
		var obj struct {
			Format string `json:"format"`
		}
		var wrapper []json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) > 0 {
			_ = json.Unmarshal(wrapper[0], &obj)
		} else {
			_ = json.Unmarshal(raw, &obj)
		}
		if obj.Format != "" {
			format = obj.Format
		}
	}
	if format != "markdown" && format != "json" {
		return nil, errInvalidParams, "format must be \"markdown\" or \"json\""
	}

	entries := s.allEntries()
	var matched []NoteEntry
	for _, e := range entries {
		if matchEntry(e, args.Filter, s.root, true) {
			matched = append(matched, e)
		}
	}
	sortEntries(matched, args.Sort)
	_, counts := s.refIndex(entries)

	if format == "json" {
		items := make([]interface{}, 0, len(matched))
		for _, e := range matched {
			items = append(items, s.listItem(e, counts, true))
		}
		data, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return nil, errInternalError, err.Error()
		}
		return string(data), 0, ""
	}

	var b strings.Builder
	b.WriteString("# DevNotes context\n\n")
	b.WriteString("_" + itoa(len(matched)) + " note(s) exported from the workspace index._\n\n")
	for _, e := range matched {
		n := e.N()
		b.WriteString("## " + n.ID + " — " + titleOr(n) + "\n\n")
		b.WriteString("- Category: " + n.Category + " · Status: " + statusOrOpen(n))
		if n.Priority != "" {
			b.WriteString(" · Priority: " + model.PriorityLabel(n.Priority))
		}
		if n.Timestamp != "" {
			b.WriteString(" · Timestamp: " + n.Timestamp)
		}
		b.WriteString("\n")
		if len(n.Tags) > 0 {
			tags := make([]string, len(n.Tags))
			for i, t := range n.Tags {
				tags[i] = "#" + t
			}
			b.WriteString("- Tags: " + strings.Join(tags, ", ") + "\n")
		}
		if len(n.Authors) > 0 {
			b.WriteString("- Authors: " + strings.Join(n.Authors, ", ") + "\n")
		}
		if len(n.Refs) > 0 {
			var refs []string
			for _, r := range n.Refs {
				refs = append(refs, r.Value)
			}
			b.WriteString("- References: " + strings.Join(refs, ", ") + "\n")
		}
		b.WriteString("- Location: " + relPath(s.root, e.Loc.File) + ":" + itoa(int(n.Range.StartLine)+1) + "\n\n")
		if n.HasBody && strings.TrimSpace(n.Body) != "" {
			b.WriteString(n.Body + "\n\n")
		}
		b.WriteString("---\n\n")
	}
	return b.String(), 0, ""
}

// reindex forces a full workspace re-scan (§13.4).
func (s *server) reindex() interface{} {
	start := time.Now()
	s.invalidateDisk()
	count := len(s.allEntries())
	return map[string]interface{}{
		"count":      count,
		"durationMs": time.Since(start).Milliseconds(),
	}
}

// renameNoteCommand backs the DUPLICATE_ID quick fix: it computes the
// WorkspaceEdit; the client applies it (and prompts for the new name).
func (s *server) renameNoteCommand(raw json.RawMessage) (interface{}, int, string) {
	var args struct {
		URI  string `json:"uri"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if len(raw) > 0 {
		var wrapper []json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) > 0 {
			raw = wrapper[0]
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errInvalidParams, "arguments must contain {\"uri\", \"id\"}"
		}
	}
	if args.URI == "" || args.ID == "" {
		return nil, errInvalidParams, "arguments must contain {\"uri\", \"id\"}"
	}
	// Find the note definition position in the target document.
	d := s.docs[args.URI]
	if d == nil {
		return nil, errInvalidParams, "Unknown document"
	}
	byteCol := uint(0)
	line := -1
	for _, loc := range d.res.NoteLocs {
		if loc.Note.ID == args.ID {
			line = int(loc.Note.IDRange.StartLine)
			byteCol = loc.Note.IDRange.StartCol
			break
		}
	}
	if line < 0 {
		return nil, errInvalidParams, "Note " + args.ID + " is not defined in this document."
	}
	name := args.Name
	if name == "" {
		name = s.noteeditSuggestedName(d, args.ID)
	}
	return s.rename(args.URI, uint(line), byteCol, name)
}

// noteeditSuggestedName proposes a rename target derived from the title.
func (s *server) noteeditSuggestedName(d *doc, id string) string {
	for _, loc := range d.res.NoteLocs {
		if loc.Note.ID == id {
			taken := map[string]bool{}
			for _, e := range s.allEntries() {
				taken[e.N().ID] = true
			}
			e := NoteEntry{Loc: loc, URI: d.uri}
			return suggestedID(&e, taken)
		}
	}
	return id
}

// pullDocumentDiagnostics implements textDocument/diagnostic.
func (s *server) pullDocumentDiagnostics(uri string) interface{} {
	d := s.docs[uri]
	if d == nil {
		return map[string]interface{}{"kind": "full", "items": []interface{}{}}
	}
	items := []interface{}{}
	for _, dg := range s.diagSet(d) {
		items = append(items, s.lspDiagFrom(d, dg))
	}
	report := map[string]interface{}{"kind": "full", "items": items}
	if d.version >= 0 {
		report["version"] = d.version
	}
	return report
}

// pullWorkspaceDiagnostics implements workspace/diagnostic.
func (s *server) pullWorkspaceDiagnostics() interface{} {
	items := []interface{}{}
	seen := map[string]bool{}
	for _, d := range s.docs {
		if seen[d.uri] {
			continue
		}
		seen[d.uri] = true
		diags := []interface{}{}
		for _, dg := range s.diagSet(d) {
			diags = append(diags, s.lspDiagFrom(d, dg))
		}
		item := map[string]interface{}{
			"uri":   d.uri,
			"kind":  "full",
			"items": diags,
		}
		if d.version >= 0 {
			item["version"] = d.version
		}
		items = append(items, item)
	}
	// Unopened files with diagnostics from the disk index.
	if s.disk != nil {
		open := map[string]bool{}
		for _, d := range s.docs {
			open[d.path] = true
		}
		for path, fi := range s.disk {
			if open[path] || seen[pathToURI(path)] || len(fi.diags) == 0 {
				continue
			}
			items = append(items, map[string]interface{}{
				"uri":   pathToURI(path),
				"kind":  "full",
				"items": s.fileDiagsToLSP(path, fi.diags),
			})
		}
	}
	return map[string]interface{}{"items": items}
}

// fileDiagsToLSP converts stored diagnostics of an unopened file.
func (s *server) fileDiagsToLSP(path string, diags []pipeline.Diag) []interface{} {
	fake := &doc{uri: pathToURI(path), path: path}
	if src := readWorkspaceFile(path); len(src) > 0 {
		fake.text = src
	}
	out := []interface{}{}
	for _, dg := range diags {
		out = append(out, s.lspDiagFrom(fake, dg))
	}
	return out
}
