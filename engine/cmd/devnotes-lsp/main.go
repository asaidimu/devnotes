// Command devnotes-lsp is a minimal LSP server exposing DevNotes validation
// diagnostics for host files and .dn files over stdio JSON-RPC.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

const serverName = "devnotes"
const serverVersion = "0.1.0"

type doc struct {
	path string
	text []byte
	res  *pipeline.FileResult
}

type server struct {
	docs map[string]*doc // keyed by LSP URI
	root string          // workspace root (from initialize rootUri)
	// note index: id -> locations, populated lazily by scanning the root.
	idx      map[string][]noteRef
	idxStale bool
}

type noteRef struct {
	File string
	Line uint
}

// ---------------------------------------------------------------------------
// JSON-RPC plumbing
// ---------------------------------------------------------------------------

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// readMessage reads one Content-Length framed JSON-RPC message from r.
func readMessage(r *bufio.Reader) ([]byte, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			length, err = strconv.Atoi(strings.Trim(strings.TrimPrefix(line, "Content-Length:"), " "))
			if err != nil || length < 0 {
				return nil, fmt.Errorf("bad Content-Length: %q", line)
			}
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w *bufio.Writer, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Flush()
}

// ---------------------------------------------------------------------------
// Request handling
// ---------------------------------------------------------------------------

func (s *server) handle(req *request, w *bufio.Writer) {
	var respond func(interface{})
	if len(req.ID) > 0 {
		respond = func(result interface{}) {
			writeMessage(w, map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  result,
			})
		}
	}

	switch req.Method {
	case "initialize":
		var p struct {
			RootURI string `json:"rootUri"`
		}
		json.Unmarshal(req.Params, &p)
		if p.RootURI != "" {
			s.root = uriToPath(p.RootURI)
		}
		respond(map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync": 1, // full sync
				"completionProvider": map[string]interface{}{
					"triggerCharacters": []string{"@", "#"},
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    serverName,
				"version": serverVersion,
			},
		})
	case "shutdown":
		respond(nil)
	case "exit":
		os.Exit(0)
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		s.docs[p.TextDocument.URI] = &doc{path: uriToPath(p.TextDocument.URI), text: []byte(p.TextDocument.Text)}
		s.idxStale = true
		s.checkAll(w)
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		json.Unmarshal(req.Params, &p)
		if d, ok := s.docs[p.TextDocument.URI]; ok && len(p.ContentChanges) > 0 {
			d.text = []byte(p.ContentChanges[len(p.ContentChanges)-1].Text)
			s.idxStale = true
			s.checkAll(w)
		}
	case "textDocument/didSave":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		if d, ok := s.docs[p.TextDocument.URI]; ok {
			if src, err := os.ReadFile(d.path); err == nil {
				d.text = src
			}
			s.idxStale = true
			s.checkAll(w)
		}
	case "textDocument/didClose":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		if _, ok := s.docs[p.TextDocument.URI]; ok {
			delete(s.docs, p.TextDocument.URI)
			s.checkAll(w)
		}
	case "textDocument/completion":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint `json:"line"`
				Character uint `json:"character"`
			} `json:"position"`
		}
		json.Unmarshal(req.Params, &p)
		items := completionItems(s, p.TextDocument.URI, p.Position.Line, p.Position.Character)
		respond(map[string]interface{}{
			"isIncomplete": false,
			"items":        items,
		})
	}
}

// completionItems returns DevNotes completions for the current line context.
func completionItems(s *server, uri string, line, col uint) []map[string]interface{} {
	var d *doc
	if dd, ok := s.docs[uri]; ok {
		d = dd
	}
	prefix := ""
	if d != nil {
		lineText := nthLine(d.text, int(line))
		ci := int(col)
		if ci > len(lineText) {
			ci = len(lineText)
		}
		prefix = string(lineText[:ci])
	}

	// Strip host-language comment prefixes so header completions work in
	// Go/TS comments too (e.g. "// @note #id obs...").
	p := strings.TrimSpace(prefix)
	p = strings.TrimPrefix(p, "//")
	p = strings.TrimPrefix(p, "/*")
	p = strings.TrimPrefix(p, "*")
	p = strings.TrimPrefix(p, "//")
	p = strings.TrimSpace(p)

	items := []map[string]interface{}{}
	lower := strings.ToLower(p)

	add := func(label string, kind int, detail, insert string) {
		item := map[string]interface{}{"label": label, "kind": kind}
		if detail != "" {
			item["detail"] = detail
		}
		if insert != "" {
			item["insertText"] = insert
			item["insertTextFormat"] = 2 // plain text
		}
		items = append(items, item)
	}

	// Inside "@note ..." header: category/field context.
	if strings.Contains(lower, "@note") {
		// After ':' we're in the title; nothing to complete.
		if strings.Contains(p, ":") {
			return items
		}
		// Right after "@note #" or "#partial": offer known IDs to reuse.
		if hashIdx := strings.Index(p, "#"); hashIdx >= 0 {
			partial := strings.TrimPrefix(p[hashIdx+1:], "#")
			refs := s.ensureIndex()
			for id, locs := range refs {
				short := strings.TrimPrefix(id, "#")
				if partial != "" && !strings.HasPrefix(short, partial) {
					continue
				}
				detail := "existing note"
				if len(locs) > 0 {
					detail = fmt.Sprintf("%s:%d", filepath.Base(locs[0].File), locs[0].Line+1)
				}
				add("#"+short, 6, detail, short)
			}
			return items
		}
		// Directives only when at the start of a directive line.
		if !strings.Contains(lower, "@author") && !strings.Contains(lower, "@see") {
			add("@author", 14, "directive", "@author ")
			add("@see", 14, "directive", "@see ")
		}
		// Categories.
		for _, c := range coreCategoriesList {
			add(c, 12, "category", c)
		}
		// Statuses.
		for _, st := range coreStatusesList {
			add(st, 12, "status", st)
		}
		// Priorities.
		for _, pr := range corePrioritiesList {
			add(pr, 12, "priority", pr)
		}
		return items
	}

	// "@see #..." or "@see #partial": complete known note IDs from the
	// workspace index.
	if strings.HasPrefix(p, "@see") {
		hashIdx := strings.Index(p, "#")
		if hashIdx >= 0 {
			partial := strings.TrimPrefix(p[hashIdx+1:], "#")
			refs := s.ensureIndex()
			for id, locs := range refs {
				short := strings.TrimPrefix(id, "#")
				if partial != "" && !strings.HasPrefix(short, partial) {
					continue
				}
				detail := "note"
				if len(locs) > 0 {
					detail = fmt.Sprintf("%s:%d", filepath.Base(locs[0].File), locs[0].Line+1)
				}
add("#"+short, 6, detail, short)
			}
			return items
		}
		add("@see", 14, "directive", "@see ")
		return items
	}

	// Bare "#" prefix on a directive-looking line: offer known note IDs.
	if strings.HasPrefix(p, "#") {
		partial := strings.TrimPrefix(p[1:], "#")
		refs := s.ensureIndex()
		for id, locs := range refs {
			short := strings.TrimPrefix(id, "#")
			if partial != "" && !strings.HasPrefix(short, partial) {
				continue
			}
			detail := "note"
			if len(locs) > 0 {
				detail = fmt.Sprintf("%s:%d", filepath.Base(locs[0].File), locs[0].Line+1)
			}
			add(id, 6, detail, short)
		}
		return items
	}

	// Directive lines (@author/@see/extension).
	if strings.HasPrefix(p, "@") {
		add("@note", 14, "note header", "@note #id category : title")
		add("@author", 14, "directive", "@author ")
		add("@see", 14, "directive", "@see ")
		return items
	}

	// Blank / other line: offer a full note header + directives.
	add("@note", 14, "note header", "@note #id category : title")
	add("@author", 14, "directive", "@author ")
	add("@see", 14, "directive", "@see ")
	for _, c := range coreCategoriesList {
		add(c, 12, "category", c)
	}
	return items
}

var coreCategoriesList = []string{"observation", "todo", "issue", "context", "lesson", "prompt"}
var coreStatusesList = []string{"open", "resolved", "wontfix", "deprecated"}
var corePrioritiesList = []string{"P0", "P1", "P2", "P3"}

// ensureIndex populates s.idx from the workspace root on first use, or after
// any document changed. Returns the current index.
func (s *server) ensureIndex() map[string][]noteRef {
	if s.idx == nil || s.idxStale {
		s.scanWorkspace()
	}
	return s.idx
}

// scanWorkspace walks the root (or the directory of the first open doc) and
// collects every note ID defined in supported files. Non-devnotes files are
// skipped; directories named like vendoring/output dirs are pruned.
func (s *server) scanWorkspace() {
	idx := map[string][]noteRef{}
	root := s.root
	if root == "" {
		for _, d := range s.docs {
			if d.path != "" {
				root = filepath.Dir(d.path)
				break
			}
		}
	}
	if root == "" {
		s.idx = idx
		s.idxStale = false
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if pipeline.Lang(filepath.Ext(path)) == "" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		res, err := pipeline.CheckFile(path, filepath.Ext(path), src)
		if err != nil {
			return nil
		}
		for _, loc := range res.NoteLocs {
			id := loc.Note.ID
			if id != "" {
				idx[id] = append(idx[id], noteRef{File: loc.File, Line: loc.Note.Range.StartLine})
			}
		}
		return nil
	})
	s.idx = idx
	s.idxStale = false
}

// nthLine returns the (0-indexed) line of b as a string, excluding its newline.
func nthLine(b []byte, line int) string {
	if line < 0 {
		return ""
	}
	start := 0
	cur := 0
	for start < len(b) && cur < line {
		nl := indexByte(b[start:], '\n')
		if nl < 0 {
			return ""
		}
		start += nl + 1
		cur++
	}
	end := indexByte(b[start:], '\n')
	if end < 0 {
		end = len(b) - start
	}
	return string(b[start : start+end])
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// checkAll re-checks every open doc, then publishes diagnostics for all of
// them (workspace checks can attribute diagnostics to any open file).
func (s *server) checkAll(w *bufio.Writer) {
	// 1. Per-file checks.
	for _, d := range s.docs {
		if len(d.text) == 0 {
			d.res = &pipeline.FileResult{File: d.path}
			continue
		}
		res, err := pipeline.CheckFile(d.path, filepath.Ext(d.path), d.text)
		if err != nil {
			d.res = &pipeline.FileResult{File: d.path}
			continue
		}
		d.res = &res
	}

	// 2. Workspace checks over all open notes.
	var noteLocs []validate.NoteLoc
	for uri, d := range s.docs {
		if d.res == nil {
			continue
		}
		for _, loc := range d.res.NoteLocs {
			noteLocs = append(noteLocs, validate.NoteLoc{File: uri, Note: loc.Note})
		}
	}
	wsByURI := map[string][]validate.WSDiag{}
	for _, dg := range validate.CheckWorkspace(noteLocs) {
		wsByURI[dg.File] = append(wsByURI[dg.File], dg)
	}

	// 3. Publish.
	for uri, d := range s.docs {
		if d.res == nil {
			continue
		}
		s.publish(uri, d.res, wsByURI[uri], w)
	}
}

// publish sends textDocument/publishDiagnostics for one doc.
func (s *server) publish(uri string, res *pipeline.FileResult, ws []validate.WSDiag, w *bufio.Writer) {
	lines := res.Lines()
	diags := []map[string]interface{}{}
	for _, d := range res.Diags {
		diags = append(diags, lspDiag(lines, d.Severity, d.Code, d.Message, d))
	}
	for _, d := range ws {
		diags = append(diags, lspDiag(lines, d.Severity, d.Code, d.Message, pipeline.Diag{
			StartLine: d.Range.StartLine, StartCol: d.Range.StartCol,
			EndLine: d.Range.EndLine, EndCol: d.Range.EndCol,
		}))
	}
	writeMessage(w, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]interface{}{
			"uri":         uri,
			"diagnostics": diags,
		},
	})
}

func lspDiag(lines [][]byte, sev validate.Severity, code, message string, d pipeline.Diag) map[string]interface{} {
	sl, sc := d.StartLine, d.StartCol
	el, ec := d.EndLine, d.EndCol
	if sl > el || (sl == el && sc > ec) {
		el, ec = sl, sc
	}
	return map[string]interface{}{
		"range": map[string]interface{}{
			"start": map[string]interface{}{"line": sl, "character": pipeline.UTF16Col(lineAt(lines, sl), sc)},
			"end":   map[string]interface{}{"line": el, "character": pipeline.UTF16Col(lineAt(lines, el), ec)},
		},
		"severity": int(sev) + 1, // LSP: 1=Error 2=Warning 3=Info 4=Hint
		"code":     code,
		"source":   serverName,
		"message":  strings.TrimSpace(message),
	}
}

func lineAt(lines [][]byte, line uint) []byte {
	if line < uint(len(lines)) {
		return lines[line]
	}
	return nil
}

// ---------------------------------------------------------------------------
// URI helpers
// ---------------------------------------------------------------------------

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	if u.Scheme != "file" {
		return uri
	}
	p := u.Path
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p
	}
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	return p
}

// ---------------------------------------------------------------------------

func main() {
	s := &server{docs: map[string]*doc{}}
	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewReader(os.Stdin)

	for {
		msg, err := readMessage(in)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "devnotes-lsp: %v\n", err)
			}
			return
		}
		var req request
		if err := json.Unmarshal(msg, &req); err != nil {
			fmt.Fprintf(os.Stderr, "devnotes-lsp: bad message: %v\n", err)
			continue
		}
		if req.Method == "" {
			continue
		}
		s.handle(&req, out)
	}
}