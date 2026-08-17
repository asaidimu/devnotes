package normalize

import "testing"

func TestLineComments(t *testing.T) {
	r := Region{
		Raw:       "// @note #x observation : Title\n// @author jane\n//\n// body text\n//     indented",
		StartLine: 10,
		StartCol:  0,
		LineStyle: true,
	}
	content, m := Normalize(r)
	want := "@note #x observation : Title\n@author jane\n\nbody text\n    indented"
	if content != want {
		t.Fatalf("content:\n got %q\nwant %q", content, want)
	}
	// header line 10 -> source line 10, col shift 3 ("// ")
	sl, sc, _, _ := m.MapRange(0, 0, 0, 0)
	if sl != 10 || sc != 3 {
		t.Fatalf("map[0,0] = %d,%d want 10,3", sl, sc)
	}
	// last line -> source line 14, shift 3 ("// " removed)
	sl, sc, _, _ = m.MapRange(4, 0, 4, 0)
	if sl != 14 || sc != 3 {
		t.Fatalf("map[4,0] = %d,%d want 14,3", sl, sc)
	}
}

func TestLineCommentNoSpace(t *testing.T) {
	r := Region{Raw: "//@note #x observation : T", StartLine: 0, StartCol: 0, LineStyle: true}
	content, _ := Normalize(r)
	if content != "@note #x observation : T" {
		t.Fatalf("got %q", content)
	}
}

func TestBlockComment(t *testing.T) {
	r := Region{
		Raw: "/*\n * @note #x observation : Title\n * @author jane\n *\n *     indented\n */",
		StartLine: 20,
		StartCol:  4,
		BlockStyle: true,
	}
	content, m := Normalize(r)
	want := "@note #x observation : Title\n@author jane\n\n    indented"
	if content != want {
		t.Fatalf("content:\n got %q\nwant %q", content, want)
	}
	// interior line 0 at source 21; col = StartCol(4) + prefix len(3) = 7
	sl, sc, _, _ := m.MapRange(0, 0, 0, 0)
	if sl != 21 || sc != 7 {
		t.Fatalf("map[0,0] = %d,%d want 21,7", sl, sc)
	}
}

func TestBlockNoDecoration(t *testing.T) {
	r := Region{
		Raw: "/*\n @note #x observation : T\n body\n*/",
		StartLine: 0,
		StartCol:  0,
		BlockStyle: true,
	}
	content, _ := Normalize(r)
	want := " @note #x observation : T\n body"
	if content != want {
		t.Fatalf("got %q want %q", content, want)
	}
}

func TestSingleLineBlock(t *testing.T) {
	r := Region{Raw: "/* @note #x observation : T */", StartLine: 0, StartCol: 0, BlockStyle: true}
	content, _ := Normalize(r)
	if content != "@note #x observation : T" {
		t.Fatalf("got %q", content)
	}
}
