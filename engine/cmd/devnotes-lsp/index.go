// Workspace index: enriched note entries merged from open documents and a
// lazily scanned view of the on-disk workspace. Powers listing, completion,
// references, rename and the export/graph commands.
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

// NoteEntry is one note known to the server, with its URI.
type NoteEntry struct {
	Loc validate.NoteLoc
	URI string
}

func (e *NoteEntry) N() model.Note { return e.Loc.Note }

// fileIndex caches the analysis of an unopened workspace file.
type fileIndex struct {
	path    string
	entries []NoteEntry
	diags   []pipeline.Diag
}

// scanDisk walks the workspace root and indexes every supported file.
// Vendored/output directories are pruned. Existing entries are replaced.
func (s *server) scanDisk() {
	s.disk = map[string]*fileIndex{}
	root := s.root
	if root == "" {
		for _, d := range s.docs {
			if d.path != "" {
				root = filepath.Dir(d.path)
				break
			}
		}
	}
	if root == "" {
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "vendor" || name == "dist" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if pipeline.Lang(filepath.Ext(path)) == "" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		res, err := pipeline.CheckFile(path, filepath.Ext(path), src)
		if err != nil {
			return nil
		}
		fi := &fileIndex{path: path, diags: res.Diags}
		for _, loc := range res.NoteLocs {
			fi.entries = append(fi.entries, NoteEntry{Loc: loc, URI: pathToURI(path)})
		}
		s.disk[path] = fi
		return nil
	})
	s.diskPublished = map[string]bool{}
}

// invalidateDisk forces the next allEntries call to re-scan.
func (s *server) invalidateDisk() {
	s.disk = nil
	s.diskPublished = nil
}

// allEntries merges open documents (current buffer state wins) with the
// on-disk index. The disk scan runs lazily on first use.
func (s *server) allEntries() []NoteEntry {
	if s.disk == nil {
		s.scanDisk()
	}
	open := map[string]bool{}
	for _, d := range s.docs {
		if d.path != "" {
			open[d.path] = true
		}
	}
	var out []NoteEntry
	for _, d := range s.docs {
		if d.res == nil {
			continue
		}
		for _, loc := range d.res.NoteLocs {
			out = append(out, NoteEntry{Loc: loc, URI: d.uri})
		}
	}
	for path, fi := range s.disk {
		if open[path] {
			continue
		}
		out = append(out, fi.entries...)
	}
	return out
}

// entriesWithID returns every definition of the given ID (with '#').
func (s *server) entriesWithID(entries []NoteEntry, id string) []NoteEntry {
	var out []NoteEntry
	for _, e := range entries {
		if e.N().ID == id {
			out = append(out, e)
		}
	}
	return out
}

// refSite is a single @use of a note ID somewhere in the workspace.
type refSite struct {
	URI    string
	Path   string
	Range  model.Range
	FromID string
}

// refIndex maps note ID -> every reference site to it, plus usage counts.
func (s *server) refIndex(entries []NoteEntry) (map[string][]refSite, map[string]int) {
	sites := map[string][]refSite{}
	counts := map[string]int{}
	for _, e := range entries {
		for _, ref := range e.N().Refs {
			if ref.Kind != "id" || ref.Value == "" {
				continue
			}
			sites[ref.Value] = append(sites[ref.Value], refSite{
				URI: e.URI, Path: e.Loc.File, Range: ref.Range, FromID: e.N().ID,
			})
			counts[ref.Value]++
		}
	}
	return sites, counts
}

// tagCounts aggregates tag usage across the workspace.
func tagCounts(entries []NoteEntry) (map[string]int, []string) {
	counts := map[string]int{}
	var order []string
	for _, e := range entries {
		for _, t := range e.N().Tags {
			if _, seen := counts[t]; !seen {
				order = append(order, t)
			}
			counts[t]++
		}
	}
	return counts, order
}

// customCategoryCounts lists non-core categories with usage counts.
func customCategoryCounts(entries []NoteEntry) map[string]int {
	counts := map[string]int{}
	for _, e := range entries {
		cat := e.N().Category
		if cat == "" || coreCategoriesList[cat] {
			continue
		}
		counts[cat]++
	}
	return counts
}

var coreCategoriesList = map[string]bool{
	"observation": true, "todo": true, "issue": true,
	"context": true, "lesson": true, "prompt": true,
}

// ---------------------------------------------------------------------------
// devnotes.list filtering and sorting
// ---------------------------------------------------------------------------

type listFilter struct {
	Category          []string
	Status            []string
	Priority          []string
	Tag               []string
	Author            string
	File              string
	Text              string
	Reference         string
	IncludeResolved   bool
	IncludeDeprecated bool
}

type listArgs struct {
	Filter      listFilter
	Sort        string
	Limit       int
	IncludeBody bool
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// matchEntry applies the AND-across-fields / OR-within-fields filter rules.
func matchEntry(e NoteEntry, f listFilter, root string, includeBody bool) bool {
	n := e.N()
	if len(f.Category) > 0 && !contains(f.Category, n.Category) {
		return false
	}
	statuses := f.Status
	if len(statuses) == 0 && !f.IncludeResolved && !f.IncludeDeprecated {
		statuses = []string{"open"}
	}
	if len(statuses) > 0 {
		st := n.Status
		if st == "" {
			st = "open" // notes default to open status
		}
		if !contains(statuses, st) {
			return false
		}
	}
	if len(f.Priority) > 0 && !contains(f.Priority, n.Priority) {
		return false
	}
	for _, tag := range f.Tag { // AND semantics across tags
		want := strings.TrimPrefix(tag, "#")
		if !contains(n.Tags, want) {
			return false
		}
	}
	if f.Author != "" {
		ok := false
		for _, a := range n.Authors {
			if a == f.Author {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.File != "" {
		rel := e.Loc.File
		if root != "" {
			if r, err := filepath.Rel(root, e.Loc.File); err == nil {
				rel = r
			}
		}
		rel = filepath.ToSlash(rel)
		if !globMatch(f.File, rel) && !globMatch(f.File, filepath.Base(rel)) {
			return false
		}
	}
	if f.Text != "" {
		hay := strings.ToLower(n.ID + " " + n.Title)
		if includeBody {
			hay += " " + strings.ToLower(n.Body)
		}
		if !strings.Contains(hay, strings.ToLower(f.Text)) {
			return false
		}
	}
	if f.Reference != "" {
		want := f.Reference
		if !strings.HasPrefix(want, "#") && !strings.Contains(want, "://") {
			want = "#" + want
		}
		ok := false
		for _, ref := range n.Refs {
			if ref.Value == want || strings.TrimPrefix(ref.Value, "#") == strings.TrimPrefix(want, "#") {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func priorityRank(p string) int {
	switch p {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	}
	return 4
}

func statusRank(st string) int {
	switch st {
	case "open", "":
		return 0
	case "wontfix":
		return 1
	case "resolved":
		return 2
	case "deprecated":
		return 3
	}
	return 4
}

// sortEntries orders entries per the devnotes.list sort modes.
func sortEntries(entries []NoteEntry, mode string) {
	less := func(a, b NoteEntry) bool { return false }
	switch mode {
	case "priority":
		less = func(a, b NoteEntry) bool {
			ra, rb := priorityRank(a.N().Priority), priorityRank(b.N().Priority)
			if ra != rb {
				return ra < rb
			}
			if a.N().ID != b.N().ID {
				return a.N().ID < b.N().ID
			}
			return a.Loc.File < b.Loc.File
		}
	case "timestamp":
		less = func(a, b NoteEntry) bool {
			ta, tb := a.N().Timestamp, b.N().Timestamp
			if ta != tb {
				if ta == "" {
					return false
				}
				if tb == "" {
					return true
				}
				return ta > tb // descending
			}
			ra, rb := priorityRank(a.N().Priority), priorityRank(b.N().Priority)
			if ra != rb {
				return ra < rb
			}
			return a.N().ID < b.N().ID
		}
	case "id":
		less = func(a, b NoteEntry) bool {
			if a.N().ID != b.N().ID {
				return a.N().ID < b.N().ID
			}
			return a.Loc.File < b.Loc.File
		}
	case "category":
		less = func(a, b NoteEntry) bool {
			if a.N().Category != b.N().Category {
				return a.N().Category < b.N().Category
			}
			ra, rb := priorityRank(a.N().Priority), priorityRank(b.N().Priority)
			if ra != rb {
				return ra < rb
			}
			return a.N().ID < b.N().ID
		}
	default: // "default"
		less = func(a, b NoteEntry) bool {
			ra, rb := priorityRank(a.N().Priority), priorityRank(b.N().Priority)
			if ra != rb {
				return ra < rb
			}
			sa, sb := statusRank(a.N().Status), statusRank(b.N().Status)
			if sa != sb {
				return sa < sb
			}
			ta, tb := a.N().Timestamp, b.N().Timestamp
			if ta != tb {
				if ta == "" {
					return false
				}
				if tb == "" {
					return true
				}
				return ta > tb
			}
			if a.N().ID != b.N().ID {
				return a.N().ID < b.N().ID
			}
			return a.Loc.File < b.Loc.File
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return less(entries[i], entries[j]) })
}

// globMatch matches a path pattern with '*' (within a segment), '?' and
// '**' (across segments) against a slash-separated path.
func globMatch(pattern, name string) bool {
	pat := strings.Split(filepath.ToSlash(pattern), "/")
	parts := strings.Split(filepath.ToSlash(name), "/")
	return globDir(pat, parts)
}

func globDir(pat, parts []string) bool {
	if len(pat) == 0 {
		return len(parts) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if globDir(pat[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	if globSeg(pat[0], parts[0]) {
		return globDir(pat[1:], parts[1:])
	}
	return false
}

// globSeg matches one path segment with '*' and '?' wildcards.
func globSeg(pat, s string) bool {
	if pat == "*" {
		return true
	}
	// Iterative wildcard match.
	var seg func(p, t string) bool
	seg = func(p, t string) bool {
		if p == "" {
			return t == ""
		}
		if p[0] == '*' {
			for i := 0; i <= len(t); i++ {
				if seg(p[1:], t[i:]) {
					return true
				}
			}
			return false
		}
		if t == "" {
			return false
		}
		if p[0] == '?' || p[0] == t[0] {
			return seg(p[1:], t[1:])
		}
		return false
	}
	return seg(pat, s)
}

// relPath renders path relative to root when possible, for display strings.
func relPath(root, path string) string {
	if root != "" {
		if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(path)
}
