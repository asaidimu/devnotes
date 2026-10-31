// Markdown rendering for hover previews, completion documentation and
// devnotes.exportContext. All content is sanitized: raw HTML is escaped and
// the server never emits javascript: links or fetches URLs.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
)

// escapeMD escapes characters that would let note text smuggle HTML into a
// markdown preview.
func escapeMD(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "`", "'")
	return r.Replace(s)
}

// excerpt truncates s to limit characters, appending an ellipsis when cut.
func excerpt(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for i := limit; i > 0; i-- { // avoid splitting a UTF-8 rune
		if (s[i] & 0xC0) != 0x80 {
			cut = i
			break
		}
	}
	return s[:cut] + "…"
}

// metaLine renders the "category · status · priority · timestamp" line.
func metaLine(n model.Note) string {
	var parts []string
	if n.Category != "" {
		parts = append(parts, "`"+n.Category+"`")
	}
	st := n.Status
	if st == "" {
		st = "open"
	}
	parts = append(parts, "`"+st+"`")
	if n.Priority != "" {
		parts = append(parts, "`"+n.Priority+"`")
	}
	if n.Timestamp != "" {
		parts = append(parts, "`"+n.Timestamp+"`")
	}
	return strings.Join(parts, " · ")
}

// containerName is the compact summary used by workspace/symbol and
// completion details.
func containerName(n model.Note) string {
	var parts []string
	if n.Category != "" {
		parts = append(parts, n.Category)
	}
	st := n.Status
	if st == "" {
		st = "open"
	}
	parts = append(parts, st)
	if n.Priority != "" {
		parts = append(parts, n.Priority)
	}
	return strings.Join(parts, " · ")
}

// renderNoteMarkdown renders the full hover/preview document for a note.
// refCount is the number of workspace references pointing at the note.
func renderNoteMarkdown(e NoteEntry, refCount int, excerptChars int, root string) string {
	n := e.N()
	var b strings.Builder

	title := n.Title
	if title == "" {
		title = n.ID + " (untitled)"
	}
	b.WriteString("### " + escapeMD(title) + "\n\n")
	b.WriteString(metaLine(n) + "\n\n")

	st := n.Status
	if st == "" {
		st = "open"
	}
	if st == "resolved" || st == "deprecated" {
		b.WriteString("> ⚠ Status: " + st + " — this note is no longer active.\n\n")
	}

	if len(n.Tags) > 0 {
		var tags []string
		for _, t := range n.Tags {
			tags = append(tags, "`#"+t+"`")
		}
		b.WriteString("**Tags:** " + strings.Join(tags, ", ") + "\n\n")
	}
	if len(n.Authors) > 0 {
		var authors []string
		for _, a := range n.Authors {
			authors = append(authors, "`"+a+"`")
		}
		b.WriteString("**Authors:** " + strings.Join(authors, ", ") + "\n\n")
	}
	if refCount > 0 {
		b.WriteString(fmt.Sprintf("**References:** %d\n\n", refCount))
	}

	if n.HasBody && strings.TrimSpace(n.Body) != "" {
		b.WriteString("---\n\n")
		body := escapeMD(n.Body)
		b.WriteString(excerpt(body, excerptChars))
		if len(body) > excerptChars && excerptChars > 0 {
			// excerpt already appended the ellipsis when it cut.
		}
		b.WriteString("\n\n")
	}

	b.WriteString("---\n")
	loc := relPath(root, e.Loc.File) + ":" + itoa(int(n.Range.StartLine)+1)
	b.WriteString("*`" + loc + "`*\n")
	return b.String()
}

// renderReferenceMarkdown renders the hover for an @see value.
func renderReferenceMarkdown(e NoteEntry, ref model.Reference, entries []NoteEntry, refCount int, excerptChars int, root string) string {
	switch ref.Kind {
	case "url":
		return "`" + escapeMD(ref.Value) + "` — external reference\n\nThe server never fetches external URLs."
	case "id":
		defs := entriesWithIDGlobal(entries, ref.Value)
		switch {
		case len(defs) == 0:
			return "`" + ref.Value + "` — unresolved reference\n\nNo note with this ID exists in the workspace. " +
				"Place the cursor on the ID and use completion ('#') to pick an existing note."
		default:
			out := renderNoteMarkdown(defs[0], refCount, excerptChars, root)
			if len(defs) > 1 {
				out += "\n\n> ⚠ Ambiguous: " + itoa(len(defs)) + " notes share this ID.\n"
				for _, d := range defs {
					out += "\n- " + relPath(root, d.Loc.File) + ":" + itoa(int(d.N().Range.StartLine)+1)
				}
			}
			return out
		}
	default:
		return "`" + escapeMD(ref.Value) + "` — reference"
	}
}

// entriesWithIDGlobal is a package-level helper mirroring server.entriesWithID
// for pre-computed entry slices.
func entriesWithIDGlobal(entries []NoteEntry, id string) []NoteEntry {
	var out []NoteEntry
	for i := range entries {
		if entries[i].N().ID == id {
			out = append(out, entries[i])
		}
	}
	return out
}

// completionDetail is the one-line detail for ID completion items.
func completionDetail(n model.Note) string {
	return containerName(n) + " — " + n.Title
}

// shortNoteDoc is the lightweight documentation attached to completion items
// (header summary only; the body excerpt is added by completionItem/resolve).
func shortNoteDoc(e NoteEntry, root string) string {
	n := e.N()
	var b strings.Builder
	title := n.Title
	if title == "" {
		title = "(untitled)"
	}
	b.WriteString("**" + escapeMD(title) + "**\n\n")
	b.WriteString(metaLine(n) + "\n")
	if len(n.Tags) > 0 {
		var tags []string
		for _, t := range n.Tags {
			tags = append(tags, "#"+t)
		}
		b.WriteString("\nTags: " + strings.Join(tags, ", ") + "\n")
	}
	return b.String()
}

// sortedCustomCategories returns custom categories, most-used first.
func sortedCustomCategories(counts map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range counts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	var out []string
	for _, it := range list {
		out = append(out, it.k)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
