package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Harness: feed JSON-RPC messages into the server, collect framed responses.
// ---------------------------------------------------------------------------

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

func responseFor(t *testing.T, frames [][]byte, id int) (json.RawMessage, bool) {
	t.Helper()
	for _, f := range frames {
		var msg struct {
			ID    int `json:"id"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(f, &msg); err != nil {
			continue
		}
		if msg.ID == id {
			return msg.Result, msg.Error == nil
		}
	}
	t.Fatalf("no response for id %d in %d frames", id, len(frames))
	return nil, false
}

func errorFor(t *testing.T, frames [][]byte, id int) (int, string, bool) {
	t.Helper()
	for _, f := range frames {
		var msg struct {
			ID    int `json:"id"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(f, &msg); err != nil {
			continue
		}
		if msg.ID == id && msg.Error != nil {
			return msg.Error.Code, msg.Error.Message, true
		}
	}
	return 0, "", false
}

type publishDiag struct {
	URI         string `json:"uri"`
	Diagnostics []struct {
		Code               string         `json:"code"`
		Severity           int            `json:"severity"`
		Message            string         `json:"message"`
		Data               map[string]any `json:"data"`
		RelatedInformation []struct {
			Message string `json:"message"`
		} `json:"relatedInformation"`
		Range struct {
			Start struct {
				Line      uint `json:"line"`
				Character uint `json:"character"`
			} `json:"start"`
		} `json:"range"`
	} `json:"diagnostics"`
}

func publishes(t *testing.T, frames [][]byte) []publishDiag {
	t.Helper()
	var out []publishDiag
	for _, f := range frames {
		var msg struct {
			Method string      `json:"method"`
			Params publishDiag `json:"params"`
		}
		if err := json.Unmarshal(f, &msg); err != nil {
			continue
		}
		if msg.Method == "textDocument/publishDiagnostics" {
			out = append(out, msg.Params)
		}
	}
	return out
}

func publishFor(t *testing.T, frames [][]byte, uri string) publishDiag {
	t.Helper()
	for _, p := range publishes(t, frames) {
		if p.URI == uri {
			return p
		}
	}
	t.Fatalf("no publishDiagnostics for %s", uri)
	return publishDiag{}
}

func codes(p publishDiag) map[string]bool {
	out := map[string]bool{}
	for _, d := range p.Diagnostics {
		out[d.Code] = true
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const testURI = "file:///ws/notes.dn"

// newTestServer returns a server with an initialized session rooted at a
// temp workspace.
func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	ws := t.TempDir()
	s := newServer()
	feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{"rootUri": "file://" + ws},
	})
	return s, ws
}

func openDoc(t *testing.T, s *server, uri, text string) [][]byte {
	t.Helper()
	return feed(t, s, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": uri, "languageId": "devnotes", "version": 1, "text": text},
		},
	})
}

// applyEdits applies LSP text edits to a document body (test-side oracle).
func applyEdits(t *testing.T, text string, edits []struct {
	Range struct {
		Start struct {
			Line      uint `json:"line"`
			Character uint `json:"character"`
		} `json:"start"`
		End struct {
			Line      uint `json:"line"`
			Character uint `json:"character"`
		} `json:"end"`
	} `json:"range"`
	NewText string `json:"newText"`
}) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	// Apply bottom-up, right-to-left for stable indices.
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		if a.Line != b.Line {
			return a.Line > b.Line
		}
		return a.Character > b.Character
	})
	for _, e := range edits {
		if int(e.Range.Start.Line) >= len(lines) {
			continue
		}
		line := []rune(lines[e.Range.Start.Line])
		sc, ec := int(e.Range.Start.Character), int(e.Range.End.Character)
		if sc > len(line) {
			sc = len(line)
		}
		if ec > len(line) {
			ec = len(line)
		}
		if e.Range.Start.Line == e.Range.End.Line {
			lines[e.Range.Start.Line] = string(line[:sc]) + e.NewText + string(line[ec:])
			continue
		}
		startLine := []rune(lines[e.Range.Start.Line])
		var endLine []rune
		if int(e.Range.End.Line) < len(lines) {
			endLine = []rune(lines[e.Range.End.Line])
		}
		merged := string(startLine[:sc]) + e.NewText
		if len(endLine) >= ec {
			merged += string(endLine[ec:])
		}
		out := append([]string{}, lines[:e.Range.Start.Line]...)
		out = append(out, merged)
		if int(e.Range.End.Line)+1 <= len(lines) {
			out = append(out, lines[e.Range.End.Line+1:]...)
		}
		lines = out
	}
	return strings.Join(lines, "\n")
}

type rawEdit struct {
	Range struct {
		Start struct {
			Line      uint `json:"line"`
			Character uint `json:"character"`
		} `json:"start"`
		End struct {
			Line      uint `json:"line"`
			Character uint `json:"character"`
		} `json:"end"`
	} `json:"range"`
	NewText string `json:"newText"`
}

func decodeEdits(t *testing.T, raw json.RawMessage) []rawEdit {
	t.Helper()
	var edits []rawEdit
	if err := json.Unmarshal(raw, &edits); err != nil {
		t.Fatalf("decode edits: %v", err)
	}
	return edits
}

// ---------------------------------------------------------------------------
// Core protocol
// ---------------------------------------------------------------------------

func TestInitializeCapabilities(t *testing.T) {
	s := &server{docs: map[string]*doc{}, cfg: defaultConfig()}
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	})
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	var resp struct {
		Result struct {
			Capabilities struct {
				TextDocumentSync struct {
					OpenClose bool `json:"openClose"`
					Change    int  `json:"change"`
					Save      struct {
						IncludeText bool `json:"includeText"`
					} `json:"save"`
				} `json:"textDocumentSync"`
				CompletionProvider struct {
					TriggerCharacters []string `json:"triggerCharacters"`
					ResolveProvider   bool     `json:"resolveProvider"`
				} `json:"completionProvider"`
				DiagnosticProvider struct {
					Identifier string `json:"identifier"`
				} `json:"diagnosticProvider"`
				RenameProvider struct {
					PrepareProvider bool `json:"prepareProvider"`
				} `json:"renameProvider"`
				ExecuteCommandProvider struct {
					Commands []string `json:"commands"`
				} `json:"executeCommandProvider"`
			} `json:"capabilities"`
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frames[0], &resp); err != nil {
		t.Fatal(err)
	}
	c := resp.Result.Capabilities
	if !c.TextDocumentSync.OpenClose || c.TextDocumentSync.Change != 1 || c.TextDocumentSync.Save.IncludeText {
		t.Fatalf("bad sync caps: %+v", c.TextDocumentSync)
	}
	if !c.CompletionProvider.ResolveProvider || !c.RenameProvider.PrepareProvider {
		t.Fatalf("missing resolve/prepare caps")
	}
	if !contains(c.ExecuteCommandProvider.Commands, "devnotes.list") ||
		!contains(c.ExecuteCommandProvider.Commands, "devnotes.reindex") {
		t.Fatalf("missing commands: %v", c.ExecuteCommandProvider.Commands)
	}
	if resp.Result.ServerInfo.Name != "devnotes" {
		t.Fatalf("bad serverInfo")
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
	if got := pathToURI("/tmp/a b.dn"); got != "file:///tmp/a%20b.dn" {
		t.Fatalf("pathToURI = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Diagnostics: clean messages, severities, related info, data
// ---------------------------------------------------------------------------

func TestDiagnosticsCleanMessagesAndSeverities(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #dup observation : First\n\n" +
		"@note #dup todo : Second\n\n" +
		"@note #ref issue : Ref\n@see #ghost\n\n" +
		"@note #nots observation : Bad status\nresolved is not a date but this header has none\n\n"
	frames := openDoc(t, s, testURI, src)
	p := publishFor(t, frames, testURI)
	c := codes(p)

	if !c["DUPLICATE_ID"] || !c["UNRESOLVED_REFERENCE"] {
		t.Fatalf("missing workspace diags: %v", p.Diagnostics)
	}
	for _, d := range p.Diagnostics {
		if strings.Contains(d.Message, "SPEC") || strings.Contains(d.Message, "§") {
			t.Fatalf("diagnostic still leaks spec refs: %q", d.Message)
		}
		if d.Message == "" {
			t.Fatalf("empty message for code %s", d.Code)
		}
		if d.Code == "DUPLICATE_ID" && d.Severity != 2 {
			t.Fatalf("DUPLICATE_ID must be Warning (2), got %d", d.Severity)
		}
		if d.Code == "DUPLICATE_ID" && len(d.RelatedInformation) == 0 {
			t.Fatalf("DUPLICATE_ID must carry relatedInformation")
		}
		if d.Code == "UNRESOLVED_REFERENCE" {
			if d.Severity != 2 {
				t.Fatalf("UNRESOLVED_REFERENCE must be Warning, got %d", d.Severity)
			}
			if d.Data["id"] != "#ghost" {
				t.Fatalf("UNRESOLVED_REFERENCE data.id = %v", d.Data["id"])
			}
		}
		if d.Code == "UNKNOWN_CATEGORY" && d.Severity != 3 {
			t.Fatalf("UNKNOWN_CATEGORY must be Info (3), got %d", d.Severity)
		}
	}
	// Severity for UNKNOWN_CATEGORY: add a note with a custom category.
	frames = openDoc(t, s, testURI, "@note #x architecture : T\n\n")
	p = publishFor(t, frames, testURI)
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == "UNKNOWN_CATEGORY" {
			found = true
			if d.Severity != 3 {
				t.Fatalf("UNKNOWN_CATEGORY severity = %d, want 3 (Info)", d.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("missing UNKNOWN_CATEGORY: %v", p.Diagnostics)
	}
}

func TestInvalidHeaderProducesDiagnostic(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #broken observation\n\n@note #good todo : Fine\n\n"
	frames := openDoc(t, s, testURI, src)
	p := publishFor(t, frames, testURI)
	if !codes(p)["INVALID_HEADER"] {
		t.Fatalf("expected INVALID_HEADER, got %v", p.Diagnostics)
	}
	for _, d := range p.Diagnostics {
		if d.Code == "INVALID_HEADER" && !strings.Contains(d.Message, "@note #id category") {
			t.Fatalf("INVALID_HEADER message should explain the shape: %q", d.Message)
		}
	}
}

func TestDidChangeRepublishes(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, "@note #x observation : T\n\n")
	frames := feed(t, s,
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
			"textDocument": map[string]any{"uri": testURI, "version": 2},
			"contentChanges": []map[string]any{
				{"text": "@note #x observation : \n\n"},
			},
		}},
	)
	p := publishFor(t, frames, testURI)
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == "MISSING_TITLE" {
			found = true
			if !strings.Contains(d.Message, "title") {
				t.Fatalf("MISSING_TITLE message should mention the title: %q", d.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected MISSING_TITLE after change, got %v", p.Diagnostics)
	}
}

func TestDuplicateAcrossOpenAndDisk(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "disk.dn"), "@note #twin todo : On disk\n\n")
	frames := openDoc(t, s, testURI, "@note #twin issue : In editor\n\n")
	// The open doc gets a DUPLICATE_ID that links to the disk file.
	p := publishFor(t, frames, testURI)
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == "DUPLICATE_ID" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected cross-file DUPLICATE_ID, got %v", p.Diagnostics)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func completionResult(t *testing.T, s *server, uri string, line, char uint) []map[string]any {
	t.Helper()
	frames := feed(t, s,
		map[string]any{"jsonrpc": "2.0", "id": 99, "method": "textDocument/completion",
			"params": map[string]any{
				"textDocument": map[string]any{"uri": uri},
				"position":     map[string]any{"line": line, "character": char},
			}},
	)
	res, ok := responseFor(t, frames, 99)
	if !ok {
		t.Fatalf("completion request failed")
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	return out.Items
}

func labels(items []map[string]any) []string {
	var out []string
	for _, it := range items {
		if l, ok := it["label"].(string); ok {
			out = append(out, l)
		}
	}
	return out
}

func itemByLabel(t *testing.T, items []map[string]any, label string) map[string]any {
	t.Helper()
	for _, it := range items {
		if it["label"] == label {
			return it
		}
	}
	t.Fatalf("no item %q in %v", label, labels(items))
	return nil
}

func TestCompletionWorkspaceIDs(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "a.go"), "package a\n\n// @note #alpha observation : Alpha note\nfunc A() {}\n\n// @note #beta todo P1 : Beta note\nfunc B() {}\n")
	writeFile(t, filepath.Join(ws, "b.dn"), "@note #gamma lesson : Gamma note\n")
	openDoc(t, s, "file:///ws/see.dn", "@see #al\n")

	items := completionResult(t, s, "file:///ws/see.dn", 0, 8)
	if !reflect.DeepEqual(labels(items), []string{"#alpha"}) {
		t.Fatalf("@see #al -> %v", labels(items))
	}
	it := itemByLabel(t, items, "#alpha")
	if it["filterText"] != "alpha" {
		t.Fatalf("filterText must omit '#': %v", it["filterText"])
	}
	if it["kind"] != float64(18) {
		t.Fatalf("kind should be Reference (18)")
	}
	if it["textEdit"] == nil {
		t.Fatalf("ID completion must carry textEdit")
	}
	if it["documentation"] == nil {
		t.Fatalf("ID completion must carry documentation preview")
	}
	// Partial "#al" replaced with the full ID.
	te := it["textEdit"].(map[string]any)
	rg := te["range"].(map[string]any)
	if rg["start"].(map[string]any)["character"] != float64(5) {
		t.Fatalf("textEdit should start at the '#': %v", rg)
	}
}

func TestCompletionNoteHeaderIDAndFields(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "a.dn"), "@note #alpha observation : Alpha\n\n")
	openDoc(t, s, "file:///ws/h.dn", "@note #\n")

	items := completionResult(t, s, "file:///ws/h.dn", 0, 7)
	if !reflect.DeepEqual(labels(items), []string{"#alpha"}) {
		t.Fatalf("@note # -> %v", labels(items))
	}

	// After '@note ': categories, statuses, priorities, today.
	openDoc(t, s, "file:///ws/f.dn", "@note \n")
	items = completionResult(t, s, "file:///ws/f.dn", 0, 6)
	seen := map[string]bool{}
	for _, l := range labels(items) {
		seen[l] = true
	}
	for _, want := range []string{"observation", "issue", "open", "deprecated", "P0", "P3", "today", "@author", "@see"} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, labels(items))
		}
	}
}

func TestCompletionTagsAndAuthors(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "a.dn"),
		"@note #one issue open P1 2026-08-10 #security,#memory : One\n@author jane\n\nbody\n\n"+
			"@note #two todo #networking : Two\n\n")
	openDoc(t, s, "file:///ws/tags.dn", "@note #new observation  #sec : T\n\n")

	items := completionResult(t, s, "file:///ws/tags.dn", 0, 25) // after "#sec"
	var got []string
	for _, l := range labels(items) {
		got = append(got, l)
	}
	if !contains(got, "#security") {
		t.Fatalf("expected #security in %v", got)
	}
	it := itemByLabel(t, items, "#security")
	if it["detail"] != "used 1 times" {
		t.Fatalf("tag detail = %v", it["detail"])
	}

	// Author completion from the index.
	openDoc(t, s, "file:///ws/auth.dn", "@note #a3 todo : T\n@author \n\n")
	items = completionResult(t, s, "file:///ws/auth.dn", 1, 8)
	if !contains(labels(items), "jane") {
		t.Fatalf("expected author jane in %v", labels(items))
	}
}

func TestCompletionSnippetsAndBodyExclusion(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #x observation : Title\n\nsome body text here\n\n"
	openDoc(t, s, testURI, src)

	// Trailing blank line outside any note: snippets offered in .dn files.
	items := completionResult(t, s, testURI, 4, 0)
	if !contains(labels(items), "@note — New DevNote") {
		t.Fatalf("expected snippet, got %v", labels(items))
	}
	// Inside the body: nothing.
	items = completionResult(t, s, testURI, 2, 5)
	if len(items) != 0 {
		t.Fatalf("completion must not fire in body: %v", labels(items))
	}
	// Host file without notes: no snippets unless enabled.
	openDoc(t, s, "file:///ws/plain.go", "package main\n\nfunc main() {}\n")
	items = completionResult(t, s, "file:///ws/plain.go", 0, 0)
	if len(items) != 0 {
		t.Fatalf("snippets must stay off in host files without notes: %v", labels(items))
	}

	// Resolve attaches the note preview to ID items.
	openDoc(t, s, "file:///ws/r.dn", "@see #x\n")
	items = completionResult(t, s, "file:///ws/r.dn", 0, 8)
	if len(items) == 0 {
		t.Fatalf("expected ID completion for #x")
	}
	id := 100
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "completionItem/resolve", "params": items[0],
	})
	res, ok := responseFor(t, frames, id)
	if !ok {
		t.Fatal("resolve failed")
	}
	var resolved map[string]any
	if err := json.Unmarshal(res, &resolved); err != nil {
		t.Fatal(err)
	}
	docMap := resolved["documentation"].(map[string]any)
	val, _ := docMap["value"].(string)
	if !strings.Contains(val, "Title") {
		t.Fatalf("resolve should attach the note preview, got %q", val)
	}
}

// ---------------------------------------------------------------------------
// Hover / definition / references / symbols / links
// ---------------------------------------------------------------------------

const hoverSrc = "@note #pool-release issue open P1 2026-08-10 #pooling : Sub-document pool ownership\n@author jane\n@see #doc-lifecycle\n@see #ghost\n\nBody about Release().\n\n@note #doc-lifecycle todo : Document lifecycle\n\ndoc body\n\n"

func TestHoverRendersNoteAndReference(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, hoverSrc)

	hoverReq := func(line, char uint) map[string]any {
		frames := feed(t, s, map[string]any{
			"jsonrpc": "2.0", "id": 7, "method": "textDocument/hover",
			"params": map[string]any{
				"textDocument": map[string]any{"uri": testURI},
				"position":     map[string]any{"line": line, "character": char},
			},
		})
		res, ok := responseFor(t, frames, 7)
		if !ok {
			t.Fatalf("hover failed")
		}
		var h map[string]any
		json.Unmarshal(res, &h)
		return h
	}

	// On the note header.
	h := hoverReq(0, 10)
	content := h["contents"].(map[string]any)["value"].(string)
	if !strings.Contains(content, "### Sub-document pool ownership") {
		t.Fatalf("hover missing title: %q", content)
	}
	if !strings.Contains(content, "`issue` · `open` · `P1` · `2026-08-10`") {
		t.Fatalf("hover missing meta line: %q", content)
	}
	if !strings.Contains(content, "**Tags:** `#pooling`") {
		t.Fatalf("hover missing tags: %q", content)
	}
	if strings.Contains(content, "SPEC") {
		t.Fatalf("hover leaks spec refs")
	}

	// On the resolvable @see value.
	h = hoverReq(2, 10)
	content = h["contents"].(map[string]any)["value"].(string)
	if !strings.Contains(content, "Document lifecycle") {
		t.Fatalf("reference hover should render target: %q", content)
	}

	// On the unresolved @see value.
	h = hoverReq(3, 10)
	content = h["contents"].(map[string]any)["value"].(string)
	if !strings.Contains(content, "unresolved reference") {
		t.Fatalf("unresolved hover: %q", content)
	}
}

func TestDefinitionAndReferences(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "target.dn"), "@note #doc-lifecycle todo : Document lifecycle\n\ndoc body\n\n")
	// The referencing note lives in the editor; the target only on disk.
	openDoc(t, s, testURI, "@note #pool-release issue open P1 : Pool ownership\n@see #doc-lifecycle\n\nbody\n\n")

	// Definition from the @see value at line 2 ("@see #doc-lifecycle").
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 8, "method": "textDocument/definition",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"position":     map[string]any{"line": 1, "character": 8},
		},
	})
	res, ok := responseFor(t, frames, 8)
	if !ok {
		t.Fatal("definition failed")
	}
	var links []struct {
		TargetURI            string `json:"targetUri"`
		TargetSelectionRange struct {
			Start struct {
				Line uint `json:"line"`
			} `json:"start"`
		} `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(res, &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || !strings.HasSuffix(links[0].TargetURI, "target.dn") || links[0].TargetSelectionRange.Start.Line != 0 {
		t.Fatalf("definition links: %+v", links)
	}

	// References to #doc-lifecycle: the @see here + the declaration.
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "textDocument/references",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": "file://" + filepath.Join(ws, "target.dn")},
			"position":     map[string]any{"line": 0, "character": 8},
			"context":      map[string]any{"includeDeclaration": true},
		},
	})
	res, ok = responseFor(t, frames, 9)
	if !ok {
		t.Fatal("references failed")
	}
	var locs []map[string]any
	json.Unmarshal(res, &locs)
	if len(locs) != 2 {
		t.Fatalf("references = %d, want 2 (site + declaration): res=%s locs=%v", len(locs), string(res), locs)
	}
}

func TestSymbolsAndLinks(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, hoverSrc)

	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 10, "method": "textDocument/documentSymbol",
		"params": map[string]any{"textDocument": map[string]any{"uri": testURI}},
	})
	res, _ := responseFor(t, frames, 10)
	var syms []struct {
		Name     string `json:"name"`
		Kind     int    `json:"kind"`
		Children []any  `json:"children"`
	}
	json.Unmarshal(res, &syms)
	if len(syms) != 2 || syms[0].Kind != 5 || len(syms[0].Children) != 0 {
		t.Fatalf("documentSymbols: %+v", syms)
	}
	if !strings.HasPrefix(syms[0].Name, "#pool-release — ") {
		t.Fatalf("symbol name: %q", syms[0].Name)
	}

	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 11, "method": "workspace/symbol",
		"params": map[string]any{"query": "pool"},
	})
	res, _ = responseFor(t, frames, 11)
	var ws []struct {
		Name string `json:"name"`
		Kind int    `json:"kind"`
	}
	json.Unmarshal(res, &ws)
	if len(ws) != 1 || ws[0].Name != "#pool-release" || ws[0].Kind != 5 {
		t.Fatalf("workspaceSymbols: %+v", ws)
	}

	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 12, "method": "textDocument/documentLink",
		"params": map[string]any{"textDocument": map[string]any{"uri": testURI}},
	})
	res, _ = responseFor(t, frames, 12)
	var links []struct {
		Target  *string `json:"target"`
		Tooltip string  `json:"tooltip"`
	}
	json.Unmarshal(res, &links)
	if len(links) != 2 {
		t.Fatalf("documentLinks: %+v", links)
	}
	if links[0].Target == nil || !strings.Contains(*links[0].Target, "target.dn") && !strings.Contains(*links[0].Target, "#L") {
		t.Fatalf("resolved link target: %+v", links[0])
	}
	if links[0].Target != nil && strings.Contains(*links[0].Target, "#doc-lifecycle") == false &&
		!strings.HasSuffix(strings.Split(*links[0].Target, "#L")[0], ".dn") {
		t.Fatalf("target should be a file URI: %q", *links[0].Target)
	}
	if links[1].Target != nil || !strings.Contains(links[1].Tooltip, "Unresolved") {
		t.Fatalf("unresolved link: %+v", links[1])
	}
}

// ---------------------------------------------------------------------------
// Code actions
// ---------------------------------------------------------------------------

func codeActions(t *testing.T, s *server, uri string, line, char uint, only []string) []map[string]any {
	t.Helper()
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": char},
			"end":   map[string]any{"line": line, "character": char},
		},
	}
	if only != nil {
		params["context"] = map[string]any{"only": only}
	}
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 20, "method": "textDocument/codeAction", "params": params,
	})
	res, ok := responseFor(t, frames, 20)
	if !ok {
		t.Fatal("codeAction failed")
	}
	var acts []map[string]any
	json.Unmarshal(res, &acts)
	return acts
}

func actionByTitle(t *testing.T, acts []map[string]any, substr string) map[string]any {
	t.Helper()
	for _, a := range acts {
		if strings.Contains(a["title"].(string), substr) {
			return a
		}
	}
	var titles []string
	for _, a := range acts {
		titles = append(titles, a["title"].(string))
	}
	t.Fatalf("no action %q among %v", substr, titles)
	return nil
}

func TestQuickFixMissingTitle(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #x observation : \n\n"
	openDoc(t, s, testURI, src)
	acts := codeActions(t, s, testURI, 0, 0, nil)
	a := actionByTitle(t, acts, "Insert placeholder title")
	if a["kind"] != "quickfix" {
		t.Fatalf("kind = %v", a["kind"])
	}
	editsRaw, _ := json.Marshal(a["edit"].(map[string]any)["changes"].(map[string]any)[testURI])
	var edits []rawEdit
	json.Unmarshal(editsRaw, &edits)
	if len(edits) != 1 || edits[0].NewText != " TITLE" {
		t.Fatalf("placeholder edit: %+v", edits)
	}
	got := applyEditsLSP(t, src, edits)
	if !strings.Contains(got, ": TITLE") {
		t.Fatalf("applied: %q", got)
	}
}

func TestStatusAndPriorityActions(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #x issue open P1 : Ship it\n\nbody\n\n"
	openDoc(t, s, testURI, src)
	acts := codeActions(t, s, testURI, 0, 10, nil)

	a := actionByTitle(t, acts, "Mark as resolved (currently: open)")
	edits := decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got := applyEditsLSP(t, src, edits)
	if !strings.Contains(got, "issue resolved P1") {
		t.Fatalf("status replace: %q", got)
	}
	if actionByTitleNoErr(acts, "Reopen") {
		t.Fatalf("Reopen must be suppressed when already open")
	}

	a = actionByTitle(t, acts, "Set priority to P0")
	edits = decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got = applyEditsLSP(t, src, edits)
	if !strings.Contains(got, "issue open P0 :") {
		t.Fatalf("priority replace: %q", got)
	}

	// Note without status/priority: insert in canonical position.
	src2 := "@note #y observation : Bare\n\n"
	openDoc(t, s, testURI, src2)
	acts = codeActions(t, s, testURI, 0, 8, nil)
	a = actionByTitle(t, acts, "Mark as wontfix (currently: open)")
	edits = decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got = applyEditsLSP(t, src2, edits)
	if !strings.Contains(got, "@note #y observation wontfix : Bare") {
		t.Fatalf("status insert: %q", got)
	}
	// Re-open the edited text so the server re-parses before the next
	// action (mirrors a real editor round-trip).
	openDoc(t, s, testURI, got)
	acts = codeActions(t, s, testURI, 0, 8, nil)
	a = actionByTitle(t, acts, "Set priority to P2")
	edits = decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got = applyEditsLSP(t, got, edits)
	if !strings.Contains(got, "@note #y observation wontfix P2 : Bare") {
		t.Fatalf("priority insert: %q", got)
	}
}

func actionByTitleNoErr(acts []map[string]any, substr string) bool {
	for _, a := range acts {
		if strings.Contains(a["title"].(string), substr) {
			return true
		}
	}
	return false
}

func TestAssignActionFromConfig(t *testing.T) {
	s, _ := newTestServer(t)
	// Configure known authors.
	feed(t, s, map[string]any{
		"jsonrpc": "2.0", "method": "workspace/didChangeConfiguration",
		"params": map[string]any{"settings": map[string]any{
			"devnotes": map[string]any{"knownAuthors": []string{"jane", "bob"}},
		}},
	})
	src := "@note #x issue : Assign me\n\n"
	openDoc(t, s, testURI, src)
	acts := codeActions(t, s, testURI, 0, 5, []string{"refactor.rewrite.devnotes"})
	a := actionByTitle(t, acts, "Assign to jane")
	edits := decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got := applyEditsLSP(t, src, edits)
	if !strings.Contains(got, "@assign jane") {
		t.Fatalf("assign edit: %q", got)
	}
}

func TestFormatNoteAction(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #x issue P1 open : Messy header\n@author jane\n@see #other\n@see #other\n\nbody  \n\n"
	openDoc(t, s, testURI, "@note #other todo : Other\n\n")
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": testURI, "languageId": "devnotes", "version": 1, "text": src}},
	})
	_ = frames
	acts := codeActions(t, s, testURI, 0, 0, nil)
	a := actionByTitle(t, acts, "Format note")
	edits := decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)[testURI]))
	got := applyEditsLSP(t, src, edits)
	if !strings.Contains(got, "@note #x issue open P1 : Messy header") {
		t.Fatalf("canonical header order: %q", got)
	}
	if strings.Count(got, "@see #other") != 1 {
		t.Fatalf("@see must dedup: %q", got)
	}
	if !strings.Contains(got, "body") || strings.Contains(got, "body  \n") {
		t.Fatalf("body trailing ws must normalize, body content preserved: %q", got)
	}
}

func TestFormatInsideGoCommentPreservesPrefix(t *testing.T) {
	s, _ := newTestServer(t)
	src := "package a\n\n// @note #x issue P1 open : Messy\n// @author jane\n//\n// body line\nfunc A() {}\n"
	openDoc(t, s, "file:///ws/a.go", src)
	acts := codeActions(t, s, "file:///ws/a.go", 2, 5, nil)
	a := actionByTitle(t, acts, "Format note")
	edits := decodeEdits(t, mustJSON(t, a["edit"].(map[string]any)["changes"].(map[string]any)["file:///ws/a.go"]))
	got := applyEditsLSP(t, src, edits)
	if !strings.Contains(got, "// @note #x issue open P1 : Messy") {
		t.Fatalf("format must keep // prefix and canonical order: %q", got)
	}
	if !strings.Contains(got, "// body line") {
		t.Fatalf("body line must keep prefix: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Rename
// ---------------------------------------------------------------------------

func TestRenameUpdatesWorkspaceReferences(t *testing.T) {
	s, ws := newTestServer(t)
	writeFile(t, filepath.Join(ws, "other.dn"), "@note #b todo : Other\n@see #alpha\n\n")
	openDoc(t, s, testURI, "@note #alpha observation : Alpha\n\n")
	id := 30
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "textDocument/rename",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"position":     map[string]any{"line": 0, "character": 8},
			"newName":      "#beta-release",
		},
	})
	res, ok := responseFor(t, frames, id)
	if !ok {
		t.Fatal("rename failed")
	}
	var we struct {
		DocumentChanges []struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version any    `json:"version"`
			} `json:"textDocument"`
			Edits []rawEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	json.Unmarshal(res, &we)
	if len(we.DocumentChanges) != 2 {
		t.Fatalf("rename must touch both files: %+v", we.DocumentChanges)
	}
	for _, dc := range we.DocumentChanges {
		got := applyEditsLSP(t, readTestFile(t, s, dc.TextDocument.URI, ws), dc.Edits)
		if strings.Contains(got, "#alpha") {
			t.Fatalf("rename left %q behind in %s", "#alpha", dc.TextDocument.URI)
		}
		if !strings.Contains(got, "#beta-release") {
			t.Fatalf("rename missing new id in %s: %q", dc.TextDocument.URI, got)
		}
	}
}

func readTestFile(t *testing.T, s *server, uri, ws string) string {
	t.Helper()
	if d, ok := s.docs[uri]; ok {
		return string(d.text)
	}
	p := uriToPath(uri)
	if strings.HasPrefix(p, "/ws/") {
		p = filepath.Join(ws, strings.TrimPrefix(p, "/ws/"))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRenameDuplicateIDFails(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #dup observation : A\n\n@note #dup todo : B\n\n"
	openDoc(t, s, testURI, src)
	id := 31
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "textDocument/rename",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"position":     map[string]any{"line": 0, "character": 8},
			"newName":      "#new",
		},
	})
	code, msg, isErr := errorFor(t, frames, id)
	if !isErr || code != -32600 || !strings.Contains(msg, "share this ID") {
		t.Fatalf("expected InvalidRequest for duplicate rename, got %d %q", code, msg)
	}
}

func TestPrepareRename(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, "@note #alpha observation : Alpha\n@see #alpha\n\n")
	id := 32
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "textDocument/prepareRename",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"position":     map[string]any{"line": 1, "character": 8},
		},
	})
	res, _ := responseFor(t, frames, id)
	var pr struct {
		Placeholder string `json:"placeholder"`
	}
	json.Unmarshal(res, &pr)
	if pr.Placeholder != "#alpha" {
		t.Fatalf("prepareRename placeholder: %q", pr.Placeholder)
	}
}

// ---------------------------------------------------------------------------
// Custom commands
// ---------------------------------------------------------------------------

func TestExecuteCommandListAndGet(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, hoverSrc)

	id := 40
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command":   "devnotes.list",
			"arguments": []any{map[string]any{"filter": map[string]any{"category": "issue"}, "sort": "priority"}},
		},
	})
	res, okList := responseFor(t, frames, id)
	if !okList {
		code, msg, _ := errorFor(t, frames, id)
		t.Fatalf("devnotes.list failed: %d %s", code, msg)
	}
	var list struct {
		Items []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Display  struct {
				Primary   string `json:"primary"`
				Secondary string `json:"secondary"`
				Tertiary  string `json:"tertiary"`
			} `json:"display"`
			Location struct {
				URI string `json:"uri"`
			} `json:"location"`
		} `json:"items"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	json.Unmarshal(res, &list)
	if len(list.Items) != 1 || list.Items[0].ID != "#pool-release" {
		t.Fatalf("list items: %+v", list.Items)
	}
	if list.Items[0].Display.Secondary != "issue · open · P1 · #pooling" {
		t.Fatalf("display.secondary: %q", list.Items[0].Display.Secondary)
	}
	if !strings.Contains(list.Items[0].Display.Tertiary, ":1") {
		t.Fatalf("display.tertiary: %q", list.Items[0].Display.Tertiary)
	}

	// get
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 41, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command": "devnotes.get", "arguments": []any{map[string]any{"id": "#pool-release"}},
		},
	})
	res, _ = responseFor(t, frames, 41)
	var item map[string]any
	json.Unmarshal(res, &item)
	if item["id"] != "#pool-release" || item["body"] == "" {
		t.Fatalf("get: %+v", item)
	}
	// Unknown ID -> null.
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 42, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command": "devnotes.get", "arguments": []any{map[string]any{"id": "#nope"}},
		},
	})
	res, _ = responseFor(t, frames, 42)
	if string(res) != "null" {
		t.Fatalf("unknown id should return null, got %s", res)
	}
}

func TestExecuteCommandGraphNextPrevExportReindex(t *testing.T) {
	s, _ := newTestServer(t)
	src := "@note #a todo : A\n@see #b\n\nA body\n\n@note #b todo : B\n@see #c\n\nB body\n\n@note #c todo : C\n\n"
	openDoc(t, s, testURI, src)

	// graph: forward from #a.
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 50, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command":   "devnotes.graph",
			"arguments": []any{map[string]any{"root": "#a", "direction": "out"}},
		},
	})
	res, _ := responseFor(t, frames, 50)
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	json.Unmarshal(res, &g)
	if len(g.Nodes) != 3 || len(g.Edges) != 2 {
		t.Fatalf("graph out: %+v / %+v", g.Nodes, g.Edges)
	}

	// nextInFile from line 0 -> note #b at line 4.
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 51, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command":   "devnotes.nextInFile",
			"arguments": []any{map[string]any{"uri": testURI, "line": 0}},
		},
	})
	res, _ = responseFor(t, frames, 51)
	var np struct {
		Range struct {
			Start struct {
				Line uint `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	json.Unmarshal(res, &np)
	if np.Range.Start.Line != 5 {
		t.Fatalf("nextInFile -> line %d, want 5", np.Range.Start.Line)
	}

	// exportContext markdown excludes nothing here; contains body + location.
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 52, "method": "workspace/executeCommand",
		"params": map[string]any{
			"command":   "devnotes.exportContext",
			"arguments": []any{map[string]any{"format": "markdown"}},
		},
	})
	res, okExport := responseFor(t, frames, 52)
	if !okExport {
		code, msg, _ := errorFor(t, frames, 52)
		t.Fatalf("devnotes.exportContext failed: %d %s", code, msg)
	}
	var md string
	json.Unmarshal(res, &md)
	if !strings.Contains(md, "# DevNotes context") || !strings.Contains(md, "A body") ||
		!strings.Contains(md, "notes.dn:1") {
		t.Fatalf("exportContext markdown incomplete: %q", md[:min(len(md), 200)])
	}

	// reindex
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 53, "method": "workspace/executeCommand",
		"params": map[string]any{"command": "devnotes.reindex"},
	})
	res, _ = responseFor(t, frames, 53)
	var ri struct {
		Count      int64 `json:"count"`
		DurationMs int64 `json:"durationMs"`
	}
	json.Unmarshal(res, &ri)
	if ri.Count != 3 {
		t.Fatalf("reindex count = %d, want 3", ri.Count)
	}
}

func TestPullDiagnostics(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, "@note #x observation : \n\n")
	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 60, "method": "textDocument/diagnostic",
		"params": map[string]any{"textDocument": map[string]any{"uri": testURI}},
	})
	res, _ := responseFor(t, frames, 60)
	var rep struct {
		Kind  string `json:"kind"`
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	json.Unmarshal(res, &rep)
	if rep.Kind != "full" || len(rep.Items) == 0 {
		t.Fatalf("pull diagnostics: %s", res)
	}
	found := false
	for _, it := range rep.Items {
		if it.Code == "MISSING_TITLE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pull diagnostics missing MISSING_TITLE: %s", res)
	}
}

// ---------------------------------------------------------------------------
// Optional capabilities: folding, selection, inlay, code lens
// ---------------------------------------------------------------------------

func TestFoldingSelectionInlayLens(t *testing.T) {
	s, _ := newTestServer(t)
	openDoc(t, s, testURI, hoverSrc)

	frames := feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 70, "method": "textDocument/foldingRange",
		"params": map[string]any{"textDocument": map[string]any{"uri": testURI}},
	})
	res, _ := responseFor(t, frames, 70)
	var folds []struct {
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		Kind      string `json:"kind"`
	}
	json.Unmarshal(res, &folds)
	if len(folds) != 2 || folds[0].Kind != "region" || folds[0].StartLine != 5 || folds[0].EndLine != 6 {
		t.Fatalf("foldingRanges: %+v", folds)
	}

	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 71, "method": "textDocument/selectionRange",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"positions":    []map[string]any{{"line": 0, "character": 9}},
		},
	})
	res, _ = responseFor(t, frames, 71)
	// The response nests range/parent; decode loosely.
	var raw []any
	json.Unmarshal(res, &raw)
	if len(raw) != 1 {
		t.Fatalf("selectionRange items: %d", len(raw))
	}
	first := raw[0].(map[string]any)
	if first["range"] == nil {
		t.Fatalf("selectionRange missing range")
	}

	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 72, "method": "textDocument/inlayHint",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": testURI},
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": 10, "character": 0},
			},
		},
	})
	res, _ = responseFor(t, frames, 72)
	var hints []struct {
		Label string `json:"label"`
	}
	json.Unmarshal(res, &hints)
	foundHeader := false
	foundRef := false
	for _, h := range hints {
		if strings.Contains(h.Label, "open · P1") {
			foundHeader = true
		}
		if strings.HasPrefix(h.Label, "↗") {
			foundRef = true
		}
	}
	if !foundHeader || !foundRef {
		t.Fatalf("inlay hints: %+v", hints)
	}

	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 73, "method": "textDocument/codeLens",
		"params": map[string]any{"textDocument": map[string]any{"uri": testURI}},
	})
	res, _ = responseFor(t, frames, 73)
	var lenses []map[string]any
	json.Unmarshal(res, &lenses)
	if len(lenses) != 2 {
		t.Fatalf("codeLens count: %d", len(lenses))
	}
	frames = feed(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 74, "method": "codeLens/resolve", "params": lenses[0],
	})
	res, _ = responseFor(t, frames, 74)
	var rl struct {
		Command struct {
			Command string `json:"command"`
			Title   string `json:"title"`
		} `json:"command"`
	}
	json.Unmarshal(res, &rl)
	if rl.Command.Command != "devnotes.get" || !strings.Contains(rl.Command.Title, "#pool-release") {
		t.Fatalf("codeLens resolve: %+v", rl)
	}
}

// ---------------------------------------------------------------------------
// Helpers shared by edit-verification tests
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func applyEditsLSP(t *testing.T, text string, edits []rawEdit) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		if a.Line != b.Line {
			return a.Line > b.Line
		}
		return a.Character > b.Character
	})
	for _, e := range edits {
		if int(e.Range.Start.Line) >= len(lines) {
			continue
		}
		line := lines[e.Range.Start.Line]
		sc, ec := int(e.Range.Start.Character), int(e.Range.End.Character)
		r := []rune(line)
		if sc > len(r) {
			sc = len(r)
		}
		if ec > len(r) {
			ec = len(r)
		}
		if e.Range.Start.Line == e.Range.End.Line {
			lines[e.Range.Start.Line] = string(r[:sc]) + e.NewText + string(r[ec:])
			continue
		}
		startPart := string(r[:sc])
		var endPart string
		if int(e.Range.End.Line) < len(lines) {
			er := []rune(lines[e.Range.End.Line])
			if ec <= len(er) {
				endPart = string(er[ec:])
			}
		}
		out := append([]string{}, lines[:e.Range.Start.Line]...)
		out = append(out, startPart+e.NewText+endPart)
		if int(e.Range.End.Line)+1 <= len(lines) {
			out = append(out, lines[e.Range.End.Line+1:]...)
		}
		lines = out
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
