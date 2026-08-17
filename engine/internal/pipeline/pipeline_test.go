package pipeline

import (
	"testing"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

func hasCode(ds []validate.WSDiag, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestEnsureTerminator(t *testing.T) {
	cases := map[string]string{
		"@note #a observation : T\n\n":           "@note #a observation : T\n\n",
		"@note #a observation : T\n":             "@note #a observation : T\n\n",
		"@note #a observation : T":               "@note #a observation : T\n\n",
		"@note #a observation : T\n\nbody\n":     "@note #a observation : T\n\nbody\n\n",
		"@note #a observation : T\n\nbody\n\n":   "@note #a observation : T\n\nbody\n\n",
	}
	for in, want := range cases {
		if got := ensureTerminator(in); got != want {
			t.Fatalf("ensureTerminator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckFileDNMissingTitle(t *testing.T) {
	res, err := CheckFile("a.dn", ".dn", []byte("@note #x observation : \n@see #ghost\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasPipe(&res, "MISSING_TITLE") {
		t.Fatalf("expected MISSING_TITLE, got %+v", res.Diags)
	}
}

func hasPipe(res *FileResult, code string) bool {
	for _, d := range res.Diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestCheckFileGoLineComment(t *testing.T) {
	src := "package p\n\n" +
		"// @note #g observation : From Go\n" +
		"// body line\n" +
		"func F() {}\n"
	res, err := CheckFile("a.go", ".go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NoteLocs) != 1 {
		t.Fatalf("want 1 note, got %d", len(res.NoteLocs))
	}
	n := res.NoteLocs[0].Note
	if n.ID != "#g" || n.Title != "From Go" {
		t.Fatalf("bad note: %+v", n)
	}
	// Header at source line 2, col 3 ("// ").
	r := n.Range
	if r.StartLine != 2 || r.StartCol != 3 {
		t.Fatalf("range %+v, want start 2,3", r)
	}
}

func TestCheckFileGoBlockComment(t *testing.T) {
	src := "package p\n\n" +
		"/*\n" +
		" * @note #b observation : From block\n" +
		" */\n" +
		"func F() {}\n"
	res, err := CheckFile("a.go", ".go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NoteLocs) != 1 {
		t.Fatalf("want 1 note, got %d", len(res.NoteLocs))
	}
	r := res.NoteLocs[0].Note.Range
	if r.StartLine != 3 || r.StartCol != 3 {
		t.Fatalf("range %+v, want start 3,3", r)
	}
}

func TestCheckFileTSExtractionNoFalsePositives(t *testing.T) {
	src := "// @note #s observation : Real note\n" +
		"const url = \"https://x.example/a//b\"; // skip this\n" +
		"const re = /a\\/\\/b/.source; // skip this too\n" +
		"const t = `t ${'//n'} ${1 / 2}`; // also skip\n"
	res, err := CheckFile("a.ts", ".ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.NoteLocs {
		if n.Note.ID != "#s" {
			t.Fatalf("unexpected note: %+v", n.Note)
		}
	}
}

func TestWorkspaceMappedRefs(t *testing.T) {
	a, err := CheckFile("a.dn", ".dn", []byte("@note #a observation : A\n@see #nope\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	ns := append([]validate.NoteLoc{}, a.NoteLocs...)
	ws := validate.CheckWorkspace(ns)
	if !hasCode(ws, "UNRESOLVED_REFERENCE") {
		t.Fatalf("expected UNRESOLVED_REFERENCE, got %+v", ws)
	}
	// The ref diagnostic must point at the source position of the @see value.
	for _, d := range ws {
		if d.Code == "UNRESOLVED_REFERENCE" {
			if d.File != "a.dn" || d.Range.StartLine != 1 || d.Range.StartCol != 5 {
				t.Fatalf("bad mapped ref range: %+v", d)
			}
		}
	}
}

func TestCheckFileSourceLines(t *testing.T) {
	res, err := CheckFile("a.dn", ".dn", []byte("@note #x observation : T\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Lines()[0]) != "@note #x observation : T" {
		t.Fatalf("line 0: %q", res.Lines()[0])
	}
}

func sampleNote(t *testing.T) model.Note {
	t.Helper()
	res, err := CheckFile("a.dn", ".dn", []byte("@note #x observation : T\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	return res.NoteLocs[0].Note
}