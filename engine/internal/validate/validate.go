// Package validate implements the DevNotes diagnostic set over parsed notes.
//
// Diagnostic messages are written for humans first: they state what is wrong
// and how to fix it, without referencing specification sections. Machine
// readable identity travels in Code and Data; cross-file context travels in
// Related.
package validate

import (
	"fmt"
	"regexp"
	"time"

	"github.com/asaidimu/devnotes/engine/internal/model"
)

// Severity levels.
type Severity int

const (
	Error Severity = iota
	Warning
	Info
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	default:
		return "info"
	}
}

// Related points at another location that helps explain this diagnostic
// (e.g. the other occurrence of a duplicated ID). File is a filesystem path;
// the LSP layer converts it to a URI.
type Related struct {
	File    string
	Message string
	Range   model.Range
}

// Diagnostic describes one problem found in a note. Range is in the
// coordinate space of the file the note lives in.
type Diagnostic struct {
	Severity Severity
	Code     string
	Message  string
	Range    model.Range
	Related  []Related
	Data     map[string]any
}

var coreCategories = map[string]bool{
	"observation": true, "todo": true, "issue": true,
	"context": true, "lesson": true, "prompt": true,
}

var coreStatuses = map[string]bool{
	"open": true, "resolved": true, "wontfix": true, "deprecated": true,
}

var corePriorities = map[string]bool{
	"P0": true, "P1": true, "P2": true, "P3": true,
}

var coreDirectives = map[string]bool{"author": true, "see": true}

// CustomCategories holds workspace-configured categories (additive to the
// core six). The LSP server populates it from configuration; CLI runs leave
// it empty.
var CustomCategories = map[string]bool{}

var timestampLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05Z07:00",
}

var validTimestampRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9]{2}:[0-9]{2}(:[0-9]{2})?([+-][0-9]{2}:[0-9]{2}|Z)?)?$`)

var validTagRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var validIDRe = regexp.MustCompile(`^#[A-Za-z0-9_-]+$`)

var validDirectiveRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// Options controls which checks run.
type Options struct {
	// WorkspaceID set enables duplicate-ID detection across all files.
	WorkspaceID string
}

// Validate runs core-field checks over a single note.
func Validate(note *model.Note) []Diagnostic {
	var out []Diagnostic
	checkID(note, &out)
	checkTitle(note, &out)
	checkStatus(note, &out)
	checkPriority(note, &out)
	checkTimestamp(note, &out)
	checkDuplicateFields(note, &out)
	checkTags(note, &out)
	checkCategory(note, &out)
	return out
}

func checkID(note *model.Note, out *[]Diagnostic) {
	if note.ID == "" || validIDRe.MatchString(note.ID) {
		return
	}
	*out = append(*out, Diagnostic{
		Severity: Error,
		Code:     "INVALID_ID",
		Message: fmt.Sprintf("Note ID %q is malformed. IDs start with '#' and may only contain letters, digits, '-' and '_' "+
			"— for example \"#fix-pool-leak\".", note.ID),
		Range: rngOr(note.IDRange, note.Range),
		Data:  map[string]any{"id": note.ID},
	})
}

func checkTitle(note *model.Note, out *[]Diagnostic) {
	if note.NoTitle {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "MISSING_TITLE",
			Message: fmt.Sprintf("Note %s has no title. Type the title after the ':' at the end of the header line, "+
				"e.g. \"@note %s %s : A short summary\".", note.ID, note.ID, note.Category),
			Range: note.Range,
			Data:  map[string]any{"id": note.ID},
		})
	}
}

func checkStatus(note *model.Note, out *[]Diagnostic) {
	if note.HasStatus && !coreStatuses[note.Status] {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "INVALID_STATUS",
			Message: fmt.Sprintf("Note %s has unknown status %q. Use one of: open, resolved, wontfix, deprecated.",
				note.ID, note.Status),
			Range: fieldRange(note, "status", note.Range),
			Data:  map[string]any{"value": note.Status},
		})
	}
}

func checkPriority(note *model.Note, out *[]Diagnostic) {
	if note.HasPri && !corePriorities[note.Priority] {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "INVALID_PRIORITY",
			Message: fmt.Sprintf("Note %s has unknown priority %q. Use P0 (critical), P1 (high), P2 (medium) or P3 (low).",
				note.ID, note.Priority),
			Range: fieldRange(note, "priority", note.Range),
			Data:  map[string]any{"value": note.Priority},
		})
	}
}

func checkTimestamp(note *model.Note, out *[]Diagnostic) {
	if !note.HasTs {
		return
	}
	if !validTimestampRe.MatchString(note.Timestamp) {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "INVALID_TIMESTAMP",
			Message: fmt.Sprintf("Note %s has timestamp %q, which is not a valid date. Use ISO 8601 form YYYY-MM-DD "+
				"(optionally with time and zone, e.g. 2026-08-10 or 2026-08-10T14:30).", note.ID, note.Timestamp),
			Range: fieldRange(note, "timestamp", note.Range),
			Data:  map[string]any{"value": note.Timestamp},
		})
		return
	}
	ok := false
	for _, layout := range timestampLayouts {
		if _, err := time.Parse(layout, note.Timestamp); err == nil {
			ok = true
			break
		}
	}
	if !ok {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "INVALID_TIMESTAMP",
			Message: fmt.Sprintf("Note %s has timestamp %q, which does not exist on the calendar (check month and day).",
				note.ID, note.Timestamp),
			Range: fieldRange(note, "timestamp", note.Range),
			Data:  map[string]any{"value": note.Timestamp},
		})
	}
}

func checkDuplicateFields(note *model.Note, out *[]Diagnostic) {
	counts := map[string]int{}
	for _, f := range note.Fields {
		counts[f.Kind]++
		if counts[f.Kind] > 1 {
			*out = append(*out, Diagnostic{
				Severity: Error,
				Code:     "DUPLICATE_FIELD",
				Message: fmt.Sprintf("Note %s repeats the %s field. Remove the extra occurrence — a header may carry "+
					"each core field at most once.", note.ID, f.Kind),
				Range: f.Range,
				Data:  map[string]any{"field": f.Kind},
			})
		}
	}
}

func checkTags(note *model.Note, out *[]Diagnostic) {
	seen := map[string]int{}
	for i, tag := range note.Tags {
		r := note.Range
		if i < len(note.TagRanges) {
			r = note.TagRanges[i]
		}
		if !validTagRe.MatchString(tag) {
			*out = append(*out, Diagnostic{
				Severity: Error,
				Code:     "INVALID_TAG",
				Message: fmt.Sprintf("Note %s has an invalid tag %q. Tags may only contain letters, digits, '-' and '_' "+
					"(the leading '#' is part of the syntax, not the name).", note.ID, tag),
				Range: r,
				Data:  map[string]any{"tag": tag},
			})
			continue
		}
		if prev, dup := seen[tag]; dup {
			*out = append(*out, Diagnostic{
				Severity: Error,
				Code:     "INVALID_TAG",
				Message: fmt.Sprintf("Note %s repeats tag %q (first used in the tags field). Remove the duplicate — "+
					"a note may carry each tag once.", note.ID, tag),
				Range: r,
				Related: []Related{{
					File:    "", // same file; the LSP layer fills the URI
					Message: "first occurrence of " + tag,
					Range:   tagRangeAt(note, prev),
				}},
				Data: map[string]any{"tag": tag},
			})
			continue
		}
		seen[tag] = i
	}
}

func checkCategory(note *model.Note, out *[]Diagnostic) {
	if note.Category == "" {
		return
	}
	if coreCategories[note.Category] || CustomCategories[note.Category] {
		return
	}
	*out = append(*out, Diagnostic{
		Severity: Info,
		Code:     "UNKNOWN_CATEGORY",
		Message: fmt.Sprintf("Note %s uses category %q, which is not a core category (observation, todo, issue, context, "+
			"lesson, prompt). Custom categories are fine — add %q to the workspace category list to record it as intentional.",
			note.ID, note.Category, note.Category),
		Range: rngOr(note.CategoryRange, note.Range),
		Data:  map[string]any{"category": note.Category},
	})
}

// CheckUnknownDirectives flags non-core directives and malformed ones.
func CheckUnknownDirectives(note *model.Note) []Diagnostic {
	var out []Diagnostic
	for _, d := range note.ExtDirs {
		if !validDirectiveRe.MatchString(d.Name) {
			out = append(out, Diagnostic{
				Severity: Warning,
				Code:     "INVALID_DIRECTIVE",
				Message: fmt.Sprintf("Note %s has a malformed directive name @%s. Directive names start with a letter and "+
					"use letters, digits, '-' or '_'.", note.ID, d.Name),
				Range: d.Range,
				Data:  map[string]any{"directive": d.Name},
			})
			continue
		}
		if coreDirectives[d.Name] {
			continue
		}
		out = append(out, Diagnostic{
			Severity: Info,
			Code:     "UNKNOWN_DIRECTIVE",
			Message: fmt.Sprintf("Note %s uses extension directive @%s. The directive is preserved as-is; register it in "+
				"the workspace configuration if it is intentional.", note.ID, d.Name),
			Range: d.Range,
			Data:  map[string]any{"directive": d.Name},
		})
	}
	return out
}

// Workspace holds notes across files for cross-file checks.
type Workspace struct {
	Notes map[string][]NoteLoc
}

// NoteLoc ties a note to a file.
type NoteLoc struct {
	File string
	Note model.Note
	// Prefixes holds, per line the note spans, the host comment prefix the
	// normalizer stripped ("" for .dn files). Purely informational for
	// validation; edit computation in the LSP layer relies on it.
	Prefixes []string
}

// WSDiag is a workspace diagnostic with the file it belongs to.
type WSDiag struct {
	Diagnostic
	File string
}

// CheckWorkspace runs workspace-wide checks (duplicate IDs, unresolvable refs).
func CheckWorkspace(ns []NoteLoc) []WSDiag {
	var out []WSDiag

	// First pass: group definitions by ID so every duplicate occurrence can
	// link to every other occurrence.
	byID := map[string][]NoteLoc{}
	var order []string
	for _, loc := range ns {
		id := loc.Note.ID
		if id == "" {
			continue
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = append(byID[id], loc)
	}

	for _, id := range order {
		locs := byID[id]
		if len(locs) < 2 {
			continue
		}
		for _, loc := range locs {
			var rel []Related
			for _, other := range locs {
				if other.File == loc.File && other.Note.Range == loc.Note.Range {
					continue
				}
				rel = append(rel, Related{
					File:    other.File,
					Message: fmt.Sprintf("%s is also defined here", id),
					Range:   other.Note.Range,
				})
			}
			out = append(out, WSDiag{
				Diagnostic: Diagnostic{
					Severity: Warning,
					Code:     "DUPLICATE_ID",
					Message: fmt.Sprintf("Note ID %s is defined %d times in the workspace, so references to it are "+
						"ambiguous. Rename all but one of these notes.", id, len(locs)),
					Range:   loc.Note.Range,
					Related: rel,
					Data:    map[string]any{"id": id, "count": len(locs)},
				},
				File: loc.File,
			})
		}
	}

	for _, loc := range ns {
		for _, ref := range loc.Note.Refs {
			if ref.Kind != "id" || ref.Value == "" {
				continue
			}
			if _, ok := byID[ref.Value]; ok {
				continue
			}
			out = append(out, WSDiag{
				Diagnostic: Diagnostic{
					Severity: Warning,
					Code:     "UNRESOLVED_REFERENCE",
					Message: fmt.Sprintf("Note %s references %s, but no note with this ID exists in the workspace. "+
						"Fix the ID (completion after '#' lists the valid ones) or create the target note.",
						loc.Note.ID, ref.Value),
					Range: ref.Range,
					Data:  map[string]any{"id": ref.Value},
				},
				File: loc.File,
			})
		}
	}
	return out
}

// fieldRange returns the source range of the named header field, falling
// back to fallback when the field has no recorded range.
func fieldRange(note *model.Note, kind string, fallback model.Range) model.Range {
	for _, f := range note.Fields {
		if f.Kind == kind {
			return f.Range
		}
	}
	return fallback
}

func tagRangeAt(note *model.Note, idx int) model.Range {
	if idx < len(note.TagRanges) {
		return note.TagRanges[idx]
	}
	return note.Range
}

func rngOr(r, fallback model.Range) model.Range {
	if r.StartLine == 0 && r.StartCol == 0 && r.EndLine == 0 && r.EndCol == 0 {
		return fallback
	}
	return r
}
