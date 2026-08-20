// Package index builds and maintains a repo-wide JSON index of DevNotes
// (SPEC 30 DevNote model) so CLI commands and AI agents can query, trace,
// and mutate notes without re-parsing the whole workspace on every call.
//
// The index is a derived cache: the source comment in the host file is
// always the record of truth. "index update" re-syncs the cache from
// source; any command that mutates a note (note add / claim / resolve)
// edits the source file first and then re-indexes just that file.
package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
)

// Version is the on-disk index schema version.
const Version = 1

// DefaultPath is the default location of the index file, relative to the
// repo root.
const DefaultPath = ".devnotes/index.json"

// Location is a note's physical position (SPEC 29). Kept separate from
// logical identity: moving a note must not change its ID.
type Location struct {
	File      string `json:"file"`
	StartLine uint   `json:"startLine"`
	StartCol  uint   `json:"startCol"`
	EndLine   uint   `json:"endLine"`
	EndCol    uint   `json:"endCol"`
}

// Entry is one indexed DevNote (SPEC 30 DevNote, plus location + hash).
type Entry struct {
	ID         string            `json:"id"`
	Category   string            `json:"category"`
	Status     string            `json:"status"`
	Priority   string            `json:"priority,omitempty"`
	Timestamp  string            `json:"timestamp,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Title      string            `json:"title"`
	Body       string            `json:"body,omitempty"`
	Authors    []string          `json:"authors,omitempty"`
	Assignee   string            `json:"assignee,omitempty"`
	References []string          `json:"references,omitempty"`
	Extensions map[string]string `json:"extensions,omitempty"`
	Location   Location          `json:"location"`
	Hash       string            `json:"hash"`
}

// FileMeta tracks per-file staleness (whole-file content hash) and which
// note IDs currently live in that file, so "index update" can detect
// added/removed/changed files cheaply.
type FileMeta struct {
	Hash    string   `json:"hash"`
	NoteIDs []string `json:"noteIds,omitempty"`
}

// Index is the full workspace snapshot.
type Index struct {
	Version     int                 `json:"version"`
	GeneratedAt time.Time           `json:"generatedAt"`
	Root        string              `json:"root"`
	Notes       map[string]Entry    `json:"notes"`
	Files       map[string]FileMeta `json:"files"`
}

// Diff summarizes what changed between two index snapshots.
type Diff struct {
	AddedNotes   []string `json:"addedNotes,omitempty"`
	ChangedNotes []string `json:"changedNotes,omitempty"`
	RemovedNotes []string `json:"removedNotes,omitempty"`
	ScannedFiles []string `json:"scannedFiles,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

func newIndex(root string) *Index {
	return &Index{
		Version:     Version,
		GeneratedAt: time.Now().UTC(),
		Root:        root,
		Notes:       map[string]Entry{},
		Files:       map[string]FileMeta{},
	}
}

// Load reads an index from disk.
func Load(path string) (*Index, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("parsing index %s: %w", path, err)
	}
	if idx.Notes == nil {
		idx.Notes = map[string]Entry{}
	}
	if idx.Files == nil {
		idx.Files = map[string]FileMeta{}
	}
	return &idx, nil
}

// Save writes the index to disk as indented JSON, creating parent
// directories as needed.
func Save(idx *Index, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// Build performs a full workspace scan under root and returns a fresh
// index. It never touches an existing index file.
func Build(root string) (*Index, Diff, error) {
	idx := newIndex(root)
	var diff Diff
	files := CollectFiles([]string{root})
	indexFiles(idx, files, &diff)
	diff.ScannedFiles = files
	for id := range idx.Notes {
		diff.AddedNotes = append(diff.AddedNotes, id)
	}
	sort.Strings(diff.AddedNotes)
	return idx, diff, nil
}

// Update re-syncs idx from source for the given paths (or the whole
// indexed root if paths is empty), reporting what changed. Files that no
// longer exist are dropped and their notes removed; files whose content
// hash is unchanged are skipped entirely.
func Update(idx *Index, paths []string) (Diff, error) {
	var diff Diff
	target := paths
	if len(target) == 0 {
		target = []string{idx.Root}
	}
	files := CollectFiles(target)
	diff.ScannedFiles = files

	seen := map[string]bool{}
	for _, f := range files {
		seen[f] = true
		content, err := os.ReadFile(f)
		if err != nil {
			diff.Warnings = append(diff.Warnings, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		hash := hashBytes(content)
		if fm, ok := idx.Files[f]; ok && fm.Hash == hash {
			continue // unchanged, skip re-parse
		}
		before := map[string]bool{}
		if fm, ok := idx.Files[f]; ok {
			for _, id := range fm.NoteIDs {
				before[id] = true
			}
		}
		var localDiff Diff
		indexFiles(idx, []string{f}, &localDiff)
		after := map[string]bool{}
		for _, id := range idx.Files[f].NoteIDs {
			after[id] = true
		}
		for id := range after {
			if before[id] {
				diff.ChangedNotes = append(diff.ChangedNotes, id)
			} else {
				diff.AddedNotes = append(diff.AddedNotes, id)
			}
		}
		for id := range before {
			if !after[id] {
				diff.RemovedNotes = append(diff.RemovedNotes, id)
				delete(idx.Notes, id)
			}
		}
		diff.Warnings = append(diff.Warnings, localDiff.Warnings...)
	}

	// Drop files that were indexed before but no longer exist under the
	// requested scope (only prune files that fall within the scanned
	// target paths, so a partial "update <path>" doesn't nuke the rest
	// of the index).
	for f, fm := range idx.Files {
		if !underAny(f, target) {
			continue
		}
		if seen[f] {
			continue
		}
		for _, id := range fm.NoteIDs {
			diff.RemovedNotes = append(diff.RemovedNotes, id)
			delete(idx.Notes, id)
		}
		delete(idx.Files, f)
	}

	idx.GeneratedAt = time.Now().UTC()
	sort.Strings(diff.AddedNotes)
	sort.Strings(diff.ChangedNotes)
	sort.Strings(diff.RemovedNotes)
	return diff, nil
}

// Status reports, without mutating idx, which indexed files are stale
// relative to the working tree (changed content hash) plus any files on
// disk that aren't indexed yet.
func Status(idx *Index) (stale, missing, untracked []string) {
	files := CollectFiles([]string{idx.Root})
	onDisk := map[string]bool{}
	for _, f := range files {
		onDisk[f] = true
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		hash := hashBytes(content)
		fm, ok := idx.Files[f]
		switch {
		case !ok:
			untracked = append(untracked, f)
		case fm.Hash != hash:
			stale = append(stale, f)
		}
	}
	for f := range idx.Files {
		if !onDisk[f] {
			missing = append(missing, f)
		}
	}
	sort.Strings(stale)
	sort.Strings(missing)
	sort.Strings(untracked)
	return
}

// indexFiles parses each file and (re)populates idx.Notes / idx.Files for
// it, replacing whatever was previously indexed for those exact files.
func indexFiles(idx *Index, files []string, diff *Diff) {
	for _, f := range files {
		ext := filepath.Ext(f)
		src, err := os.ReadFile(f)
		if err != nil {
			diff.Warnings = append(diff.Warnings, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		// Clear whatever this file previously contributed.
		if fm, ok := idx.Files[f]; ok {
			for _, id := range fm.NoteIDs {
				delete(idx.Notes, id)
			}
		}
		res, err := pipeline.CheckFile(f, ext, src)
		if err != nil {
			diff.Warnings = append(diff.Warnings, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		var ids []string
		for _, loc := range res.NoteLocs {
			e := toEntry(f, loc.Note)
			if _, dup := idx.Notes[e.ID]; dup {
				diff.Warnings = append(diff.Warnings, fmt.Sprintf("duplicate note id %q (%s)", e.ID, f))
			}
			idx.Notes[e.ID] = e
			ids = append(ids, e.ID)
		}
		sort.Strings(ids)
		idx.Files[f] = FileMeta{Hash: hashBytes(src), NoteIDs: ids}
	}
}

func toEntry(file string, n model.Note) Entry {
	e := Entry{
		ID:       strings.TrimPrefix(n.ID, "#"),
		Category: n.Category,
		Status:   statusOrDefault(n),
		Title:    n.Title,
		Body:     n.Body,
		Authors:  n.Authors,
		Tags:     n.Tags,
		Location: Location{
			File:      file,
			StartLine: n.Range.StartLine,
			StartCol:  n.Range.StartCol,
			EndLine:   n.Range.EndLine,
			EndCol:    n.Range.EndCol,
		},
	}
	if n.HasPri {
		e.Priority = n.Priority
	}
	if n.HasTs {
		e.Timestamp = n.Timestamp
	}
	for _, ref := range n.Refs {
		e.References = append(e.References, ref.Value)
	}
	for _, d := range n.ExtDirs {
		if d.Name == "assignee" && d.HasValue {
			e.Assignee = strings.TrimSpace(d.Value)
			continue
		}
		if e.Extensions == nil {
			e.Extensions = map[string]string{}
		}
		if d.HasValue {
			e.Extensions[d.Name] = d.Value
		} else {
			e.Extensions[d.Name] = ""
		}
	}
	e.Hash = contentHash(e)
	return e
}

func statusOrDefault(n model.Note) string {
	if n.HasStatus {
		return n.Status
	}
	return "open" // SPEC 11 default
}

func contentHash(e Entry) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		e.ID, e.Category, e.Status, e.Priority, e.Timestamp,
		strings.Join(e.Tags, ","), e.Title, e.Body)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

func underAny(file string, targets []string) bool {
	for _, t := range targets {
		rel, err := filepath.Rel(t, file)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// CollectFiles gathers every file under the given paths that has a
// DevNotes-supporting extension (mirrors the logic used by `devnotes
// check`, shared here so index and check stay consistent).
func CollectFiles(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if pipeline.Lang(filepath.Ext(p)) != "" && !seen[p] {
				out = append(out, p)
				seen[p] = true
			}
			continue
		}
		_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != p && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if pipeline.Lang(filepath.Ext(path)) != "" && !seen[path] {
				out = append(out, path)
				seen[path] = true
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// EnsureNoDuplicateIDs is a convenience wrapper around validate.CheckWorkspace
// used by callers that want workspace-level diagnostics (duplicate IDs,
// unresolved @see references) computed directly from an Index rather than
// a fresh parse.
func EnsureNoDuplicateIDs(idx *Index) []string {
	seen := map[string][]string{}
	for id, e := range idx.Notes {
		seen[id] = append(seen[id], e.Location.File)
	}
	var out []string
	for id, files := range seen {
		if len(files) > 1 {
			out = append(out, fmt.Sprintf("%s: %s", id, strings.Join(files, ", ")))
		}
	}
	sort.Strings(out)
	return out
}
