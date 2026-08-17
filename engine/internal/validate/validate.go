// Package validate implements SPEC 31 diagnostics over parsed DevNotes.
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

// Diagnostic is a SPEC 31 diagnostic. Range is within normalized content.
type Diagnostic struct {
	Severity Severity
	Code     string
	Message  string
	Range    model.Range
}

var coreCategories = map[string]bool{
	"observation": true, "todo": true, "issue": true,
	"context": true, "lesson": true, "prompt": true,
}

var coreStatuses = map[string]bool{
	"open": true, "resolved": true, "wontfix": true, "deprecated": true,
}

var coreDirectives = map[string]bool{"author": true, "see": true}

var timestampLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05Z07:00",
}

var validTimestampRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9]{2}:[0-9]{2}(:[0-9]{2})?([+-][0-9]{2}:[0-9]{2}|Z)?)?$`)

// Options controls which checks run.
type Options struct {
	// WorkspaceID set enables duplicate-ID detection across all files.
	WorkspaceID string
}

// Validate runs core-field checks over a single note.
func Validate(note *model.Note) []Diagnostic {
	var out []Diagnostic
	checkTitle(note, &out)
	checkStatus(note, &out)
	checkTimestamp(note, &out)
	checkDuplicateFields(note, &out)
	checkCategory(note, &out)
	return out
}

func checkTitle(note *model.Note, out *[]Diagnostic) {
	if note.NoTitle {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "MISSING_TITLE",
			Message:  fmt.Sprintf("note %s has no title (SPEC 15/26)", note.ID),
			Range:    note.Range,
		})
	}
}

func checkStatus(note *model.Note, out *[]Diagnostic) {
	if note.HasStatus && !coreStatuses[note.Status] {
		*out = append(*out, Diagnostic{
			Severity: Error,
			Code:     "INVALID_STATUS",
			Message:  fmt.Sprintf("note %s has unknown status %q (SPEC 11)", note.ID, note.Status),
			Range:    note.Range,
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
			Message:  fmt.Sprintf("note %s has malformed timestamp %q (SPEC 13)", note.ID, note.Timestamp),
			Range:    note.Range,
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
			Message:  fmt.Sprintf("note %s has non-ISO-8601 timestamp %q (SPEC 13)", note.ID, note.Timestamp),
			Range:    note.Range,
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
				Message:  fmt.Sprintf("note %s repeats core field %s (SPEC 21)", note.ID, f.Kind),
				Range:    f.Range,
			})
		}
	}
}

func checkCategory(note *model.Note, out *[]Diagnostic) {
	if note.Category != "" && !coreCategories[note.Category] {
		*out = append(*out, Diagnostic{
			Severity: Warning,
			Code:     "UNKNOWN_CATEGORY",
			Message:  fmt.Sprintf("note %s uses non-core category %q (SPEC 9.2)", note.ID, note.Category),
			Range:    note.Range,
		})
	}
}

// CheckUnknownDirectives flags non-core directives.
func CheckUnknownDirectives(note *model.Note) []Diagnostic {
	var out []Diagnostic
	for _, d := range note.ExtDirs {
		if !coreDirectives[d.Name] {
			out = append(out, Diagnostic{
				Severity: Info,
				Code:     "UNKNOWN_DIRECTIVE",
				Message:  fmt.Sprintf("note %s uses extension directive @%s (SPEC 19)", note.ID, d.Name),
				Range:    d.Range,
			})
		}
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
}

// WSDiag is a workspace diagnostic with the file it belongs to.
type WSDiag struct {
	Diagnostic
	File string
}

// CheckWorkspace runs workspace-wide checks (duplicate IDs, unresolvable refs).
// refsByID is the set of note IDs present in the workspace.
func CheckWorkspace(ns []NoteLoc) []WSDiag {
	var out []WSDiag

	ids := map[string]string{} // id -> file
	for _, loc := range ns {
		id := loc.Note.ID
		if id == "" {
			continue
		}
		if f, dup := ids[id]; dup {
			out = append(out, WSDiag{
				Diagnostic: Diagnostic{
					Severity: Error,
					Code:     "DUPLICATE_ID",
					Message:  fmt.Sprintf("note ID %s is duplicated across %s and %s (SPEC 8.2)", id, f, loc.File),
					Range:    loc.Note.Range,
				},
				File: loc.File,
			})
		} else {
			ids[id] = loc.File
		}
	}

	for _, loc := range ns {
		for _, ref := range loc.Note.Refs {
			if ref.Kind != "id" {
				continue
			}
			if ref.Value != "" {
				if _, ok := ids[ref.Value]; !ok {
					out = append(out, WSDiag{
						Diagnostic: Diagnostic{
							Severity: Warning,
							Code:     "UNRESOLVED_REFERENCE",
							Message:  fmt.Sprintf("note %s references unresolvable ID %s (SPEC 18)", loc.Note.ID, ref.Value),
							Range:    ref.Range,
						},
						File: loc.File,
					})
				}
			}
		}
	}
	return out
}
