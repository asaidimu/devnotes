package tsextract

import "testing"

func comments(t *testing.T, src string) []Span {
	t.Helper()
	return Extract([]byte(src))
}

func TestBasicComments(t *testing.T) {
	src := "// hello\nconst x = 1\n/* block */\n// world\n"
	sp := comments(t, src)
	if len(sp) != 3 {
		t.Fatalf("want 3 comments, got %d: %+v", len(sp), sp)
	}
	if sp[0].Text != "// hello" || !sp[0].LineStyle {
		t.Fatalf("bad span 0: %+v", sp[0])
	}
	if sp[1].Text != "/* block */" || sp[1].LineStyle {
		t.Fatalf("bad span 1: %+v", sp[1])
	}
	if sp[0].StartLine != 0 || sp[1].StartLine != 2 || sp[2].StartLine != 3 {
		t.Fatalf("bad lines: %+v", sp)
	}
}

func TestSkipsStringComments(t *testing.T) {
	src := "const s = \"a // b\"\n// real\nconst t = '// not';\n"
	sp := comments(t, src)
	if len(sp) != 1 {
		t.Fatalf("want 1 comment, got %d: %+v", len(sp), sp)
	}
	if sp[0].StartLine != 1 {
		t.Fatalf("wrong line: %+v", sp[0])
	}
}

func TestSkipsTemplateComment(t *testing.T) {
	src := "const t = `a // b ${ 1 // inner\n + 2 } c`\n// real\n"
	sp := comments(t, src)
	if len(sp) != 2 {
		t.Fatalf("want 2 comments, got %d: %+v", len(sp), sp)
	}
	// inner comment inside ${} and the final // real (line 2, template spans 2 lines).
	if sp[0].StartLine != 0 || sp[1].StartLine != 2 {
		t.Fatalf("wrong lines: %+v", sp)
	}
}

func TestSkipsRegex(t *testing.T) {
	src := "const re = /a\\/\\/b$/\n// c\nconst re2 = /[/]/g\n// d\n"
	sp := comments(t, src)
	if len(sp) != 2 {
		t.Fatalf("want 2 comments, got %d: %+v", len(sp), sp)
	}
	if sp[0].StartLine != 1 || sp[1].StartLine != 3 {
		t.Fatalf("wrong lines: %+v", sp)
	}
}

func TestRegexVsDivision(t *testing.T) {
	src := "const x = 1 / 2 // a\n" +
		"const y = (a * 4) / 2 // b\n" +
		"if (y) { z = x++ / 2 } // c\n" +
		"return /re/.test(x) // d\n"
	sp := comments(t, src)
	wantLines := []uint{0, 1, 2, 3}
	if len(sp) != len(wantLines) {
		t.Fatalf("want %d comments, got %d: %+v", len(wantLines), len(sp), sp)
	}
	for i, l := range wantLines {
		if sp[i].StartLine != l {
			t.Fatalf("span %d on line %d, want %d", i, sp[i].StartLine, l)
		}
	}
}

func TestArrowAndTernaryRegex(t *testing.T) {
	src := "const f = () => /x/.test(v) // a\nconst g = ok ? /y/ : null // b\n"
	sp := comments(t, src)
	if len(sp) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(sp), sp)
	}
	if sp[0].StartLine != 0 || sp[1].StartLine != 1 {
		t.Fatalf("wrong lines: %+v", sp)
	}
}

func TestTemplateWithNestedExpr(t *testing.T) {
	src := "const s = `${a} ${b / 2}` // a\n"
	sp := comments(t, src)
	if len(sp) != 1 {
		t.Fatalf("want 1 comment, got %d: %+v", len(sp), sp)
	}
}