package goextract

import (
	"strings"
	"testing"
)

func TestExtractGroupsContiguousLineComments(t *testing.T) {
	src := `package p

// @note #a observation : one
// @author jane
//
// body line

func F() {}

// @note #b todo : two
func G() {}
`
	regions, err := Extract("", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 {
		t.Fatalf("want 2 regions, got %d", len(regions))
	}
	a := regions[0]
	if !a.LineStyle || a.StartLine != 2 || a.StartCol != 0 {
		t.Fatalf("bad region a: %+v", a)
	}
	wantA := "// @note #a observation : one\n// @author jane\n//\n// body line"
	if a.Raw != wantA {
		t.Fatalf("region a:\n got %q\nwant %q", a.Raw, wantA)
	}
	b := regions[1]
	if b.StartLine != 9 {
		t.Fatalf("region b starts line %d, want 9", b.StartLine)
	}
}

func TestExtractSeparatesByCode(t *testing.T) {
	src := `package p

// @note #a observation : one
func A() {}

// @note #b todo : two
func B() {}
`
	regions, err := Extract("", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 {
		t.Fatalf("want 2 regions, got %d", len(regions))
	}
	if regions[0].StartLine != 2 || regions[1].StartLine != 5 {
		t.Fatalf("wrong starts: %d, %d", regions[0].StartLine, regions[1].StartLine)
	}
}

func TestExtractBlockComment(t *testing.T) {
	src := `package p

/*
 * @note #b observation : block
 * body line
 */
func B() {}
`
	regions, err := Extract("", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 {
		t.Fatalf("want 1 region, got %d", len(regions))
	}
	if !regions[0].BlockStyle {
		t.Fatalf("want block style: %+v", regions[0])
	}
	if regions[0].StartLine != 2 {
		t.Fatalf("block starts line %d, want 2", regions[0].StartLine)
	}
	if !strings.HasPrefix(regions[0].Raw, "/*") {
		t.Fatalf("raw should keep markers: %q", regions[0].Raw)
	}
}
