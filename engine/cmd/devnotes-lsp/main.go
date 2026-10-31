// Command devnotes-lsp is the DevNotes Language Server: it validates
// DevNotes in host files and .dn files, serves hover previews, workspace
// listing, context-aware completion, code actions, rename, formatting and a
// set of devnotes.* commands over stdio JSON-RPC.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/pipeline"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

const serverName = "devnotes"
const serverVersion = "0.3.0"

// doc is one open document.
type doc struct {
	uri     string
	path    string
	text    []byte
	version int64 // -1 when unknown
	res     *pipeline.FileResult
}

// utf16Col converts a byte column on a line of this doc to UTF-16 units.
func (d *doc) utf16Col(line, col uint) uint {
	lines := docLines(d)
	if int(line) < len(lines) {
		return pipeline.UTF16Col(lines[line], col)
	}
	return col
}

type server struct {
	docs map[string]*doc // keyed by LSP URI
	root string          // workspace root (from initialize)
	cfg  config

	// disk caches the analysis of unopened workspace files; nil until the
	// first scan.
	disk          map[string]*fileIndex
	diskPublished map[string]bool // URIs already pushed diagnostics for
}

func newServer() *server {
	return &server{
		docs: map[string]*doc{},
		cfg:  defaultConfig(),
	}
}

// readWorkspaceFile reads a file from disk, returning nil on error.
func readWorkspaceFile(path string) []byte {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return src
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
	respondErr := func(code int, msg string) {
		writeError(w, req.ID, code, msg)
	}

	switch req.Method {
	case "initialize":
		var p struct {
			RootURI          string `json:"rootUri"`
			RootPath         string `json:"rootPath"`
			WorkspaceFolders []struct {
				URI string `json:"uri"`
			} `json:"workspaceFolders"`
		}
		json.Unmarshal(req.Params, &p)
		switch {
		case p.RootURI != "":
			s.root = uriToPath(p.RootURI)
		case len(p.WorkspaceFolders) > 0:
			s.root = uriToPath(p.WorkspaceFolders[0].URI)
		case p.RootPath != "":
			s.root = p.RootPath
		}
		s.cfg = loadConfigFromRoot(s.root)
		s.cfg.syncCustomCategories()
		s.invalidateDisk()
		respond(map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync": map[string]interface{}{
					"openClose": true,
					"change":    1, // full sync
					"save":      map[string]interface{}{"includeText": false},
				},
				"diagnosticProvider": map[string]interface{}{
					"identifier":            "devnotes",
					"interFileDependencies": true,
					"workspaceDiagnostics":  true,
				},
				"hoverProvider": true,
				"completionProvider": map[string]interface{}{
					"triggerCharacters": []string{"@", "#", ":", ",", " "},
					"resolveProvider":   true,
				},
				"definitionProvider":      true,
				"referencesProvider":      true,
				"documentSymbolProvider":  true,
				"workspaceSymbolProvider": true,
				"documentLinkProvider": map[string]interface{}{
					"resolveProvider": false,
				},
				"renameProvider": map[string]interface{}{
					"prepareProvider": true,
				},
				"codeActionProvider": map[string]interface{}{
					"codeActionKinds": []string{
						"quickfix",
						"refactor.rewrite.devnotes",
						"source.fixAll.devnotes",
					},
					"resolveProvider": false,
				},
				"documentFormattingProvider":      true,
				"documentRangeFormattingProvider": true,
				"foldingRangeProvider":            true,
				"selectionRangeProvider":          true,
				"inlayHintProvider":               map[string]interface{}{},
				"codeLensProvider":                map[string]interface{}{"resolveProvider": true},
				"executeCommandProvider": map[string]interface{}{
					"commands": []string{
						"devnotes.list",
						"devnotes.get",
						"devnotes.graph",
						"devnotes.nextInFile",
						"devnotes.prevInFile",
						"devnotes.exportContext",
						"devnotes.reindex",
						"devnotes.renameNote",
					},
				},
				"workspace": map[string]interface{}{
					"workspaceFolders": map[string]interface{}{
						"supported": true, "changeNotifications": true,
					},
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    serverName,
				"version": serverVersion,
			},
		})
	case "initialized":
		// notification, no-op
	case "shutdown":
		respond(nil)
	case "exit":
		os.Exit(0)
	case "workspace/didChangeConfiguration":
		var p struct {
			Settings map[string]interface{} `json:"settings"`
		}
		json.Unmarshal(req.Params, &p)
		if err := s.cfg.apply(p.Settings); err != nil {
			s.notify(w, "window/showMessage", map[string]interface{}{
				"type": 2, "message": "devnotes: " + err.Error(),
			})
		}
		s.cfg.syncCustomCategories()
	case "workspace/didChangeWorkspaceFolders":
		var p struct {
			Event struct {
				Added []struct {
					URI string `json:"uri"`
				} `json:"added"`
			} `json:"event"`
		}
		json.Unmarshal(req.Params, &p)
		if len(p.Event.Added) > 0 {
			s.root = uriToPath(p.Event.Added[0].URI)
			s.invalidateDisk()
		}
	case "workspace/didChangeWatchedFiles":
		// A file changed on disk; drop the cached index so the next lookup
		// re-scans.
		s.invalidateDisk()
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Text    string `json:"text"`
				Version int64  `json:"version"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		d := &doc{uri: p.TextDocument.URI, path: uriToPath(p.TextDocument.URI),
			text: []byte(p.TextDocument.Text), version: p.TextDocument.Version}
		s.docs[p.TextDocument.URI] = d
		s.refreshDoc(d)
		s.afterDocumentEvent(w)
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int64  `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		json.Unmarshal(req.Params, &p)
		if d, ok := s.docs[p.TextDocument.URI]; ok && len(p.ContentChanges) > 0 {
			d.text = []byte(p.ContentChanges[len(p.ContentChanges)-1].Text)
			if p.TextDocument.Version != 0 {
				d.version = p.TextDocument.Version
			}
			s.refreshDoc(d)
			s.afterDocumentEvent(w)
		}
	case "textDocument/didSave":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		if d, ok := s.docs[p.TextDocument.URI]; ok {
			if src := readWorkspaceFile(d.path); src != nil {
				d.text = src
			}
			s.refreshDoc(d)
			s.afterDocumentEvent(w)
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
			s.afterDocumentEvent(w)
		}
	case "textDocument/diagnostic":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.pullDocumentDiagnostics(p.TextDocument.URI))
	case "workspace/diagnostic":
		respond(s.pullWorkspaceDiagnostics())
	case "textDocument/hover":
		var pos posParams
		json.Unmarshal(req.Params, &pos)
		respond(s.hover(pos.TextDocument.URI, pos.Position.Line, pos.Position.Character))
	case "textDocument/definition":
		var pos posParams
		json.Unmarshal(req.Params, &pos)
		respond(s.definition(pos.TextDocument.URI, pos.Position.Line, pos.Position.Character))
	case "textDocument/references":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint `json:"line"`
				Character uint `json:"character"`
			} `json:"position"`
			Context struct {
				IncludeDeclaration bool `json:"includeDeclaration"`
			} `json:"context"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.references(p.TextDocument.URI, p.Position.Line, p.Position.Character, p.Context.IncludeDeclaration))
	case "textDocument/documentSymbol":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.documentSymbols(p.TextDocument.URI))
	case "workspace/symbol":
		var p struct {
			Query string `json:"query"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.workspaceSymbols(p.Query))
	case "textDocument/documentLink":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.documentLinks(p.TextDocument.URI))
	case "textDocument/foldingRange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.foldingRanges(p.TextDocument.URI))
	case "textDocument/selectionRange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Positions []lspPos `json:"positions"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.selectionRanges(p.TextDocument.URI, p.Positions))
	case "textDocument/inlayHint":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
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
		}
		json.Unmarshal(req.Params, &p)
		respond(s.inlayHints(p.TextDocument.URI, p.Range.Start.Line, p.Range.End.Line))
	case "textDocument/codeLens":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.codeLens(p.TextDocument.URI))
	case "codeLens/resolve":
		var lens struct {
			Data json.RawMessage `json:"data"`
		}
		json.Unmarshal(req.Params, &lens)
		respond(s.resolveCodeLens(lens.Data))
	case "textDocument/completion":
		var pos posParams
		json.Unmarshal(req.Params, &pos)
		items := s.completionItems(pos.TextDocument.URI, pos.Position.Line, pos.Position.Character)
		if items == nil {
			items = []map[string]interface{}{}
		}
		respond(map[string]interface{}{"isIncomplete": false, "items": items})
	case "completionItem/resolve":
		var item map[string]interface{}
		json.Unmarshal(req.Params, &item)
		respond(s.resolveCompletion(item))
	case "textDocument/codeAction":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
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
			Context struct {
				Only []string `json:"only"`
			} `json:"context"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.codeActions(p.TextDocument.URI,
			p.Range.Start.Line, p.Range.Start.Character,
			p.Range.End.Line, p.Range.End.Character, p.Context.Only))
	case "textDocument/rename":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint `json:"line"`
				Character uint `json:"character"`
			} `json:"position"`
			NewName string `json:"newName"`
		}
		json.Unmarshal(req.Params, &p)
		result, code, msg := s.rename(p.TextDocument.URI, p.Position.Line, p.Position.Character, p.NewName)
		if code != 0 {
			respondErr(code, msg)
			return
		}
		respond(result)
	case "textDocument/prepareRename":
		var pos posParams
		json.Unmarshal(req.Params, &pos)
		respond(s.prepareRename(pos.TextDocument.URI, pos.Position.Line, pos.Position.Character))
	case "textDocument/formatting":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(req.Params, &p)
		respond(s.formatting(p.TextDocument.URI, nil))
	case "textDocument/rangeFormatting":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
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
		}
		json.Unmarshal(req.Params, &p)
		rng := modelRange(p.Range.Start.Line, p.Range.Start.Character, p.Range.End.Line, p.Range.End.Character)
		respond(s.formatting(p.TextDocument.URI, &rng))
	case "workspace/executeCommand":
		var p struct {
			Command   string            `json:"command"`
			Arguments []json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		var raw json.RawMessage
		if len(p.Arguments) > 0 {
			raw = p.Arguments[0]
		}
		switch p.Command {
		case "devnotes.list":
			result, code, msg := s.listNotes(raw)
			if code != 0 {
				respondErr(code, msg)
			} else {
				respond(result)
			}
		case "devnotes.get":
			result, code, msg := s.getNote(raw)
			if code != 0 {
				respondErr(code, msg)
			} else {
				respond(result)
			}
		case "devnotes.graph":
			result, code, msg := s.graph(raw)
			if code != 0 {
				respondErr(code, msg)
			} else {
				respond(result)
			}
		case "devnotes.nextInFile", "devnotes.prevInFile":
			var args struct {
				URI  string `json:"uri"`
				Line int    `json:"line"`
				Wrap *bool  `json:"wrap"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				respondErr(errInvalidParams, "arguments must contain {\"uri\", \"line\"}")
				return
			}
			wrap := true
			if args.Wrap != nil {
				wrap = *args.Wrap
			}
			respond(s.nextPrevInFile(args.URI, args.Line, wrap, p.Command == "devnotes.nextInFile"))
		case "devnotes.exportContext":
			result, code, msg := s.exportContext(raw)
			if code != 0 {
				respondErr(code, msg)
			} else {
				respond(result)
			}
		case "devnotes.reindex":
			respond(s.reindex())
		case "devnotes.renameNote":
			result, code, msg := s.renameNoteCommand(raw)
			if code != 0 {
				respondErr(code, msg)
			} else {
				respond(result)
			}
		default:
			respondErr(errInvalidRequest, "Unknown command: "+p.Command)
		}
	default:
		if len(req.ID) > 0 {
			respondErr(errMethodNotFound, "Method not supported: "+req.Method)
		}
	}
}

// notify sends a server-initiated notification.
func (s *server) notify(w *bufio.Writer, method string, params interface{}) {
	_ = writeMessage(w, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

type posParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      uint `json:"line"`
		Character uint `json:"character"`
	} `json:"position"`
}

// modelRange builds a byte-coordinate range for range formatting. Clients
// send UTF-16 columns; whole-note formatting only compares line numbers, so
// the character columns are converted against the doc lazily at call sites.
func modelRange(sl, sc, el, ec uint) model.Range {
	return model.Range{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

// refreshDoc re-runs the analysis pipeline for one document.
func (s *server) refreshDoc(d *doc) {
	if len(d.text) == 0 {
		d.res = &pipeline.FileResult{File: d.path}
		return
	}
	res, err := pipeline.CheckFile(d.path, filepath.Ext(d.path), d.text)
	if err != nil {
		d.res = &pipeline.FileResult{File: d.path}
		return
	}
	d.res = &res
}

// afterDocumentEvent republishes diagnostics after any document lifecycle
// event. Open documents are always published; unopened files that carry
// diagnostics (per-file or workspace-level) are published once per scan
// generation so cross-file problems become visible without spamming.
func (s *server) afterDocumentEvent(w *bufio.Writer) {
	entries := s.allEntries()
	ws := map[string][]validate.WSDiag{}
	for _, dg := range validate.CheckWorkspace(entryLocs(entries)) {
		ws[dg.File] = append(ws[dg.File], dg)
	}

	for uri, d := range s.docs {
		items := []interface{}{}
		if d.res != nil {
			for _, dg := range d.res.Diags {
				items = append(items, s.lspDiagFrom(d, dg))
			}
		}
		for _, dg := range ws[d.path] {
			items = append(items, s.lspDiagFrom(d, pipeline.Diag{
				File: d.path, Severity: dg.Severity, Code: dg.Code, Message: dg.Message,
				StartLine: dg.Range.StartLine, StartCol: dg.Range.StartCol,
				EndLine: dg.Range.EndLine, EndCol: dg.Range.EndCol,
				Related: dg.Related, Data: dg.Data,
			}))
		}
		s.notify(w, "textDocument/publishDiagnostics", map[string]interface{}{
			"uri":         uri,
			"diagnostics": items,
		})
	}

	if s.disk == nil {
		return
	}
	open := map[string]bool{}
	for _, d := range s.docs {
		open[d.path] = true
	}
	for path, fi := range s.disk {
		uri := pathToURI(path)
		if open[path] || s.diskPublished[uri] {
			continue
		}
		has := len(fi.diags) > 0 || len(ws[path]) > 0
		if !has {
			continue
		}
		s.diskPublished[uri] = true
		items := s.fileDiagsToLSP(path, fi.diags)
		fake := &doc{uri: uri, path: path, text: readWorkspaceFile(path)}
		for _, dg := range ws[path] {
			items = append(items, s.lspDiagFrom(fake, pipeline.Diag{
				File: path, Severity: dg.Severity, Code: dg.Code, Message: dg.Message,
				StartLine: dg.Range.StartLine, StartCol: dg.Range.StartCol,
				EndLine: dg.Range.EndLine, EndCol: dg.Range.EndCol,
				Related: dg.Related, Data: dg.Data,
			}))
		}
		s.notify(w, "textDocument/publishDiagnostics", map[string]interface{}{
			"uri":         uri,
			"diagnostics": items,
		})
	}
}

// entryLocs strips entries down to the NoteLocs the workspace checker wants.
func entryLocs(entries []NoteEntry) []validate.NoteLoc {
	out := make([]validate.NoteLoc, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Loc)
	}
	return out
}

// ---------------------------------------------------------------------------

func main() {
	s := newServer()
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
