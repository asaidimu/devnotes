package model

import (
	"strings"
	"testing"
)

// Directives are recognized only at the start of a normalized line, and may
// appear after body text. The separator is no longer syntactically required.
func TestDirectivesAnywhereWithInlineSee(t *testing.T) {
	src := "@note #x issue : No separator needed\n" +
		"Body mentions @see #target and @see https://example.com/a.\n" +
		"  @status open is indented body text.\n" +
		"@priority high\n" +
		"\\@priority low is escaped body text.\n"

	notes, err := Build([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	n := notes[0]
	if !n.HasPri || n.Priority != "high" {
		t.Fatalf("priority = %q (has=%v), want high", n.Priority, n.HasPri)
	}
	if n.HasStatus {
		t.Fatalf("indented @status must be body, got status %q", n.Status)
	}
	if len(n.InlineRefs) != 2 {
		t.Fatalf("inline refs = %+v, want 2", n.InlineRefs)
	}
	if len(n.Refs) != 2 {
		t.Fatalf("refs = %+v, want 2", n.Refs)
	}
	if !strings.Contains(n.Body, "@see #target") {
		t.Fatalf("body lost inline @see: %q", n.Body)
	}
	if !strings.Contains(n.Body, "  @status open is indented body text.") {
		t.Fatalf("body lost indented directive-looking line: %q", n.Body)
	}
	if !strings.Contains(n.Body, "@priority low is escaped body text.") {
		t.Fatalf("body did not unescape \\@: %q", n.Body)
	}
	if strings.Contains(n.Body, "priority high") {
		t.Fatalf("directive line leaked into body: %q", n.Body)
	}
}

func TestInlineSeeInTitle(t *testing.T) {
	notes, err := Build([]byte("@note #x issue : Follow @see #other, then continue\n"))
	if err != nil {
		t.Fatal(err)
	}
	n := notes[0]
	if len(n.InlineRefs) != 1 || n.InlineRefs[0].Value != "#other" {
		t.Fatalf("inline refs = %+v, want #other", n.InlineRefs)
	}
	if got := n.Title; got != "Follow @see #other, then continue" {
		t.Fatalf("title = %q", got)
	}
}

func TestBodyDoesNotStartWithCanonicalBlankLine(t *testing.T) {
	notes, err := Build([]byte("@note #x issue : T\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := notes[0].Body; got != "body" {
		t.Fatalf("body = %q, want %q", got, "body")
	}
}
