// JSON-RPC plumbing for the devnotes LSP server: message framing, URI and
// position conversion helpers.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

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

// writeError sends a JSON-RPC error response for a request ID.
func writeError(w *bufio.Writer, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		return
	}
	_ = writeMessage(w, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error":   map[string]interface{}{"code": code, "message": message},
	})
}

// JSON-RPC error codes used by the server.
const (
	errParse          = -32700
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInvalidRequest = -32600
	errInternalError  = -32603
	errServerNotInit  = -32002
)

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

func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// utf16ToByte converts a UTF-16 code-unit column (as sent by LSP clients)
// into a byte column within line.
func utf16ToByte(line []byte, units uint) uint {
	var u, i uint
	for i < uint(len(line)) {
		if u >= units {
			return i
		}
		c := line[i]
		var size, width uint = 1, 1
		switch {
		case c&0xF8 == 0xF0:
			size, width = 4, 2
		case c&0xF0 == 0xE0:
			size, width = 3, 1
		case c&0xE0 == 0xC0:
			size, width = 2, 1
		}
		if u+width > units {
			return i // inside an astral pair; snap to char start
		}
		u += width
		i += size
	}
	return uint(len(line))
}

// rngContains reports whether the 0-based (line, byteCol) position falls
// inside r (end exclusive).
func rngContains(r struct{ StartLine, StartCol, EndLine, EndCol uint }, line, col uint) bool {
	if line < r.StartLine || line > r.EndLine {
		return false
	}
	if line == r.StartLine && col < r.StartCol {
		return false
	}
	if line == r.EndLine && col >= r.EndCol {
		return false
	}
	return true
}

// rangesIntersect reports whether two line/col ranges overlap.
func rangesIntersect(a, b struct{ StartLine, StartCol, EndLine, EndCol uint }) bool {
	// a.start > b.end  or  b.start > a.end  -> disjoint
	if posLess(b.EndLine, b.EndCol, a.StartLine, a.StartCol) ||
		posLess(a.EndLine, a.EndCol, b.StartLine, b.StartCol) {
		return false
	}
	return true
}

// posLess reports whether (l1,c1) < (l2,c2).
func posLess(l1, c1, l2, c2 uint) bool {
	if l1 != l2 {
		return l1 < l2
	}
	return c1 < c2
}
