package validate

import (
	"strings"
	"testing"

	"github.com/asaidimu/devnotes/engine/internal/model"
)

func mustNotes(t *testing.T, content string) []model.Note {
	t.Helper()
	notes, err := model.Build([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

func codes(ds []Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func has(ds []Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func hasWS(ds []WSDiag, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func codesWS(ds []WSDiag) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func validateContent(t *testing.T, content string) []Diagnostic {
	t.Helper()
	var all []Diagnostic
	for _, n := range mustNotes(t, content) {
		all = append(all, Validate(&n)...)
		all = append(all, CheckUnknownDirectives(&n)...)
	}
	return all
}

func TestMinimalNoteClean(t *testing.T) {
	ds := validateContent(t, "@note #x observation : Example\n\n")
	if len(ds) != 0 {
		t.Fatalf("expected no diagnostics, got %v", codes(ds))
	}
}

func TestFullHeaderClean(t *testing.T) {
	content := "@note #x issue : Possible XSS\n@status open\n@priority high\n@created 2026-08-10\n@tags #security #input\n\n"
	ds := validateContent(t, content)
	if len(ds) != 0 {
		t.Fatalf("expected no diagnostics, got %v", codes(ds))
	}
}

func TestDuplicatePriority(t *testing.T) {
	ds := validateContent(t, "@note #x issue : Something\n@priority high\n@priority low\n\n")
	if !has(ds, "DUPLICATE_FIELD") {
		t.Fatalf("expected DUPLICATE_FIELD, got %v", codes(ds))
	}
}

func TestDuplicateStatus(t *testing.T) {
	ds := validateContent(t, "@note #x issue : Something\n@status open\n@status resolved\n\n")
	if !has(ds, "DUPLICATE_FIELD") {
		t.Fatalf("expected DUPLICATE_FIELD, got %v", codes(ds))
	}
}

func TestMissingTitle(t *testing.T) {
	ds := validateContent(t, "@note #x observation : \n\n")
	if !has(ds, "MISSING_TITLE") {
		t.Fatalf("expected MISSING_TITLE, got %v", codes(ds))
	}
}

func TestInvalidTimestampValue(t *testing.T) {
	ds := validateContent(t, "@note #x observation : T\n@created 2026-13-45\n\n")
	if !has(ds, "INVALID_TIMESTAMP") {
		t.Fatalf("expected INVALID_TIMESTAMP, got %v", codes(ds))
	}
}

func TestEqualsDirectivesClean(t *testing.T) {
	notes := mustNotes(t, "@note #x issue : T\n@status=open\n@priority=critical\n@tags=#a,#b\n@created=2026-08-10\n\n")
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	n := notes[0]
	if n.Status != "open" || !n.HasStatus {
		t.Fatalf("status=open was not parsed: %+v", n)
	}
	if n.Priority != "critical" || !n.HasPri {
		t.Fatalf("priority=critical was not parsed: %+v", n)
	}
	if len(n.Tags) != 2 || n.Tags[0] != "a" || n.Tags[1] != "b" {
		t.Fatalf("tags were not parsed: %+v", n.Tags)
	}
	if n.Timestamp != "2026-08-10" || !n.HasTs {
		t.Fatalf("created=... was not parsed: %+v", n)
	}
	ds := validateContent(t, "@note #x issue : T\n@status=open\n@priority=critical\n@tags=#a,#b\n@created=2026-08-10\n\n")
	if len(ds) != 0 {
		t.Fatalf("expected no diagnostics, got %v", codes(ds))
	}
}
func TestInvalidStatusAndPriority(t *testing.T) {
	ds := validateContent(t, "@note #x issue : T\n@status closed\n@priority P4\n\n")
	if !has(ds, "INVALID_STATUS") || !has(ds, "INVALID_PRIORITY") {
		t.Fatalf("expected INVALID_STATUS and INVALID_PRIORITY, got %v", codes(ds))
	}
}

func TestLegacyPriorityAlias(t *testing.T) {
	ds := validateContent(t, "@note #x issue : T\n@priority P1\n\n")
	if len(ds) != 0 {
		t.Fatalf("expected no diagnostics, got %v", codes(ds))
	}
}

func TestUnknownCategory(t *testing.T) {
	ds := validateContent(t, "@note #x architecture : T\n\n")
	if !has(ds, "UNKNOWN_CATEGORY") {
		t.Fatalf("expected UNKNOWN_CATEGORY, got %v", codes(ds))
	}
}

func TestUnknownDirective(t *testing.T) {
	ds := validateContent(t, "@note #x observation : T\n@component storage\n\nbody\n")
	if !has(ds, "UNKNOWN_DIRECTIVE") {
		t.Fatalf("expected UNKNOWN_DIRECTIVE, got %v", codes(ds))
	}
}

func TestWorkspaceDuplicateID(t *testing.T) {
	a := mustNotes(t, "@note #x observation : A\n\n")[0]
	b := mustNotes(t, "@note #x todo : B\n\n")[0]
	ds := CheckWorkspace([]NoteLoc{{File: "a.dn", Note: a}, {File: "b.dn", Note: b}})
	if !hasWS(ds, "DUPLICATE_ID") {
		t.Fatalf("expected DUPLICATE_ID, got %v", codesWS(ds))
	}
}

func TestUnresolvedReference(t *testing.T) {
	content := "@note #a observation : A\n@see #missing-note\n\n"
	loc := NoteLoc{File: "a.dn", Note: mustNotes(t, content)[0]}
	// Also add a resolvable ref to ensure it is not flagged.
	content2 := "@note #b todo : B\n@see #a\n\n"
	loc2 := NoteLoc{File: "b.dn", Note: mustNotes(t, content2)[0]}
	ds := CheckWorkspace([]NoteLoc{loc, loc2})
	if !hasWS(ds, "UNRESOLVED_REFERENCE") {
		t.Fatalf("expected UNRESOLVED_REFERENCE, got %v", codesWS(ds))
	}
	if len(ds) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %v", codesWS(ds))
	}
}

func TestDirectivesAndBodyModel(t *testing.T) {
	content := "@note #x observation : T\n@author jane\n@see #y\n@see https://example.com\n\nbody line\n  indented\n"
	n := mustNotes(t, content)[0]
	if n.ID != "#x" || n.Category != "observation" || n.Title != "T" {
		t.Fatalf("header wrong: %+v", n)
	}
	if len(n.Authors) != 1 || n.Authors[0] != "jane" {
		t.Fatalf("authors wrong: %v", n.Authors)
	}
	if len(n.Refs) != 2 || n.Refs[0].Kind != "id" || n.Refs[1].Kind != "url" {
		t.Fatalf("refs wrong: %+v", n.Refs)
	}
	want := "body line\n  indented"
	if n.Body != want {
		t.Fatalf("body wrong: %q", n.Body)
	}
	if !strings.Contains(n.Body, "  indented") {
		t.Fatalf("body indentation lost: %q", n.Body)
	}
}

func TestEscapedNoteBodyIsBody(t *testing.T) {
	content := "@note #x observation : T\n\n\\@note is literal.\n"
	n := mustNotes(t, content)[0]
	if !strings.Contains(n.Body, "@note") {
		t.Fatalf("body wrong: %q", n.Body)
	}
}
