package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Legacy positional headers are rejected by the current grammar, so the
// parser cannot see their metadata. The index must still recover it rather
// than silently dropping status, priority, tags, dates, and extensions.
func TestBuildPreservesLegacyHeaderMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.dn")
	src := "@note #legacy issue resolved P1 #tag1,#tag2 2026-01-02 owner : Legacy title\n" +
		"@see #target\n\nbody line\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, _, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := idx.Notes["legacy"]
	if !ok {
		t.Fatalf("legacy note missing from index: %+v", idx.Notes)
	}
	if e.ID != "legacy" || e.Category != "issue" {
		t.Errorf("identity = %q/%q, want legacy/issue", e.ID, e.Category)
	}
	if e.Status != "resolved" {
		t.Errorf("status = %q, want resolved", e.Status)
	}
	if e.Priority != "high" {
		t.Errorf("priority = %q, want high (P1 alias)", e.Priority)
	}
	if e.Timestamp != "2026-01-02" {
		t.Errorf("timestamp = %q, want 2026-01-02", e.Timestamp)
	}
	if want := []string{"tag1", "tag2"}; !reflect.DeepEqual(e.Tags, want) {
		t.Errorf("tags = %v, want %v", e.Tags, want)
	}
	if got := e.Extensions["owner"]; got != "" {
		t.Errorf("extension owner = %q, want empty value", got)
	}
	if want := []string{"#target"}; !reflect.DeepEqual(e.References, want) {
		t.Errorf("references = %v, want %v", e.References, want)
	}
	if e.Title != "Legacy title" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Body != "body line" {
		t.Errorf("body = %q", e.Body)
	}
	if e.Hash != contentHash(e) {
		t.Errorf("hash was computed before legacy metadata was recovered")
	}
}

func TestBuildIndexesInlineSeeReferences(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "inline.dn")
	src := "@note #x issue : Inline refs\n" +
		"Body mentions @see #target and @see https://example.com/spec.\n" +
		"@priority high\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, _, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	e := idx.Notes["x"]
	want := []string{"#target", "https://example.com/spec"}
	if !reflect.DeepEqual(e.References, want) {
		t.Fatalf("references = %v, want %v", e.References, want)
	}
	if e.Priority != "high" {
		t.Fatalf("priority = %q, want high", e.Priority)
	}
}
