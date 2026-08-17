// Command devnotes-lsp is a minimal LSP server exposing DevNotes validation
// diagnostics for host files and .dn files over stdio JSON-RPC.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
		respond(map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync": 1, // full sync
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
	}
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