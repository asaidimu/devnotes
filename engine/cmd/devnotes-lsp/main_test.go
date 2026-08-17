package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// feed sends messages to the server and returns the response frames.
func feed(t *testing.T, s *server, msgs ...any) [][]byte {
	t.Helper()
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, m := range msgs {
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		s.handle(&req, w)
	}
	var frames [][]byte
	r := bufio.NewReader(&buf)
	for {
		msg, err := readMessage(r)
		if err != nil {
			break
		}
		frames = append(frames, msg)
	}
	return frames
}

func TestInitialize(t *testing.T) {
	s := &server{docs: map[string]*doc{}}
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	})
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	var resp struct {
		ID int `json:"id"`
		Result struct {
			Capabilities struct {
				TextDocumentSync int `json:"textDocumentSync"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frames[0], &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != 1 || resp.Result.Capabilities.TextDocumentSync != 1 {
		t.Fatalf("bad init response: %s", frames[0])
	}
}

func TestPublishDiagnostics(t *testing.T) {
	s := &server{docs: map[string]*doc{}}
	uri := "file:///tmp/a.dn"
	src := "@note #dup observation : First\n\n@note #dup todo : Second\n\n@note #ref issue : Ref\n@see #ghost\n\n"
	frames := feed(t, s,
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri, "languageId": "devnotes", "version": 1, "text": src},
		}},
	)
	if len(frames) != 1 {
		t.Fatalf("want 1 publish frame, got %d", len(frames))
	}
	var msg struct {
		Method string `json:"method"`
		Params struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Code     string `json:"code"`
				Severity int    `json:"severity"`
				Range    struct {
					Start struct{ Line, Character uint } `json:"start"`
				} `json:"range"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frames[0], &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("unexpected method %q", msg.Method)
	}
	codes := map[string]bool{}
	var unresolvedStart uint
	for _, d := range msg.Params.Diagnostics {
		codes[d.Code] = true
		if d.Code == "UNRESOLVED_REFERENCE" {
			unresolvedStart = d.Range.Start.Character
		}
	}
	if !codes["DUPLICATE_ID"] {
		t.Fatalf("missing DUPLICATE_ID: %+v", msg.Params.Diagnostics)
	}
	if unresolvedStart != 5 {
		t.Fatalf("UNRESOLVED_REFERENCE should start at char 5, got %d", unresolvedStart)
	}
}

func TestDidChangeRepublishes(t *testing.T) {
	s := &server{docs: map[string]*doc{}}
	uri := "file:///tmp/b.dn"
	feed(t, s,
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri, "text": "@note #x observation : T\n\n"},
		}},
	)
	frames := feed(t, s,
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri, "version": 2},
			"contentChanges": []map[string]any{
				{"text": "@note #x observation : \n\n"},
			},
		}},
	)
	if len(frames) != 1 {
		t.Fatalf("want 1 publish, got %d", len(frames))
	}
	var msg struct {
		Params struct {
			Diagnostics []struct {
				Code string `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	json.Unmarshal(frames[0], &msg)
	found := false
	for _, d := range msg.Params.Diagnostics {
		if d.Code == "MISSING_TITLE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected MISSING_TITLE after change, got %s", frames[0])
	}
}

func TestURIHelpers(t *testing.T) {
	if got := uriToPath("file:///tmp/a%20b.dn"); got != "/tmp/a b.dn" {
		t.Fatalf("uriToPath = %q", got)
	}
	if got := uriToPath("file://localhost/etc/x"); got != "/etc/x" {
		t.Fatalf("uriToPath localhost = %q", got)
	}
	if got := uriToPath("untitled:foo"); got != "untitled:foo" {
		t.Fatalf("uriToPath non-file = %q", got)
	}
}