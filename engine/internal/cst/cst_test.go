package cst

import "testing"

func TestParseMinimalNote(t *testing.T) {
	src := []byte("@note #x observation : Example\n\n")
	tree, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	root := tree.RootNode()
	if got := root.ToSexp(); got == "" {
		t.Fatal("empty sexp")
	} else {
		t.Logf("sexp: %s", got)
	}
}

func TestParseMalformedRecovers(t *testing.T) {
	src := []byte("@note broken ...\n@note #b observation : B\n\nbody of b\n")
	tree, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	root := tree.RootNode()
	sexp := root.ToSexp()
	t.Logf("sexp: %s", sexp)
}
