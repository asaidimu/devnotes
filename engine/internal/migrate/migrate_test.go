package migrate

import "testing"

func TestContentDN(t *testing.T) {
	src := "@note #x issue P1 open #a,#b 2026-08-10 : Title\n\nbody\n"
	got, changed := Content([]byte(src))
	if !changed {
		t.Fatal("expected a change")
	}
	want := "@note #x issue : Title\n@status open\n@priority high\n@tags #a #b\n@created 2026-08-10\n\nbody\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestContentGoComment(t *testing.T) {
	src := "package a\n\n// @note #x todo P2 : Fix it\n//\n// body\nfunc A() {}\n"
	got, changed := Content([]byte(src))
	if !changed {
		t.Fatal("expected a change")
	}
	want := "package a\n\n// @note #x todo : Fix it\n// @priority medium\n//\n// body\nfunc A() {}\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestContentIdempotent(t *testing.T) {
	src := "@note #x issue : Title\n@priority high\n\nbody\n"
	got, changed := Content([]byte(src))
	if changed {
		t.Fatalf("new-format note should not change: %s", got)
	}
}

func TestEscapedNoteIgnored(t *testing.T) {
	src := "@note #x observation : T\n\n\\@note #y issue P1 : literal\n"
	got, changed := Content([]byte(src))
	if changed {
		t.Fatalf("escaped note should not change: %s", got)
	}
}

func TestNormalizeEqualsDirectives(t *testing.T) {
	src := "@note #x issue : Title\n@status=open\n@priority = high\n@tags=#a,#b\n@created=2026-11-02\n\nbody\n"
	got, changed := Content([]byte(src))
	if !changed {
		t.Fatal("expected a change")
	}
	want := "@note #x issue : Title\n@status open\n@priority high\n@tags #a #b\n@created 2026-11-02\n\nbody\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
