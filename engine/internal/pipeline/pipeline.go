// Package pipeline drives comment extraction, DevNotes parsing, and
// validation for host source files and standalone .dn files.
package pipeline

import (
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/host/goextract"
	"github.com/asaidimu/devnotes/engine/internal/host/tsextract"
	"github.com/asaidimu/devnotes/engine/internal/model"
	"github.com/asaidimu/devnotes/engine/internal/normalize"
	"github.com/asaidimu/devnotes/engine/internal/validate"
)

// Diag is a source-mapped diagnostic. Coordinates are 0-based, byte columns.
type Diag struct {
	File      string
	Severity  validate.Severity
	Code      string
	Message   string
	StartLine uint
	StartCol  uint
	EndLine   uint
	EndCol    uint
}

// FileResult holds per-file diagnostics plus notes for workspace checks.
type FileResult struct {
	File      string
	Diags     []Diag
	NoteLocs  []validate.NoteLoc
	contentLines [][]byte
}

// Lang returns the devnotes host language for a file extension.
func Lang(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".ts":
		return "ts"
	case ".tsx":
		return "tsx"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "js"
	case ".dn", ".devnotes":
		return "dn"
	}
	return ""
}

// CheckFile runs the full pipeline on one file.
func CheckFile(filename, ext string, src []byte) (FileResult, error) {
	lang := Lang(ext)
	var res FileResult
	res.File = filename
	res.contentLines = splitLines(src)

	regions, err := regionsFor(lang, src)
	if err != nil {
		return res, err
	}

	var notes []model.Note
	for _, region := range regions {
		content, sm := normalize.Normalize(region)
		ns, err := model.Build([]byte(ensureTerminator(content)))
		if err != nil {
			return res, err
		}
		for i := range ns {
			n := &ns[i]
			for _, d := range validate.Validate(n) {
				res.Diags = append(res.Diags, mapDiag(filename, d, sm))
			}
			for _, d := range validate.CheckUnknownDirectives(n) {
				res.Diags = append(res.Diags, mapDiag(filename, d, sm))
			}
			// Workspace checks need source-mapped note positions.
			mapNote(n, &sm)
			notes = append(notes, *n)
		}
	}
	for _, n := range notes {
		res.NoteLocs = append(res.NoteLocs, validate.NoteLoc{File: filename, Note: n})
	}
	return res, nil
}

// ensureTerminator guarantees the normalized content parses as complete notes:
// the grammar requires a blank separator line after metadata, so content must
// end with a newline and, if it has no trailing blank line, one is appended.
func ensureTerminator(s string) string {
	if strings.HasSuffix(s, "\n\n") {
		return s
	}
	if strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s + "\n\n"
}

func regionsFor(lang string, src []byte) ([]normalize.Region, error) {
	switch lang {
	case "go":
		return goextract.Extract("", src)
	case "ts", "tsx", "js":
		return tsextract.Regions(src), nil
	default: // dn: whole file is already normalized content
		return []normalize.Region{{Raw: string(src), StartLine: 0, StartCol: 0}}, nil
	}
}

func splitLines(src []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range src {
		if c == '\n' {
			line := src[start:i]
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(src) {
		out = append(out, src[start:])
	}
	return out
}

func mapDiag(file string, d validate.Diagnostic, sm normalize.SourceMap) Diag {
	sl, sc, el, ec := sm.MapRange(d.Range.StartLine, d.Range.StartCol, d.Range.EndLine, d.Range.EndCol)
	return Diag{file, d.Severity, d.Code, d.Message, sl, sc, el, ec}
}

func sourceRange(r model.Range, sm normalize.SourceMap) model.Range {
	sl, sc, el, ec := sm.MapRange(r.StartLine, r.StartCol, r.EndLine, r.EndCol)
	return model.Range{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

// mapNote remaps every range carried by a note into host source coordinates so
// that workspace diagnostics (duplicate IDs, unresolved refs) point at real
// source positions.
func mapNote(n *model.Note, sm *normalize.SourceMap) {
	n.Range = sourceRange(n.Range, *sm)
	for i := range n.Fields {
		n.Fields[i].Range = sourceRange(n.Fields[i].Range, *sm)
	}
	for i := range n.Ext {
		n.Ext[i].Range = sourceRange(n.Ext[i].Range, *sm)
	}
	for i := range n.Refs {
		n.Refs[i].Range = sourceRange(n.Refs[i].Range, *sm)
	}
	for i := range n.ExtDirs {
		n.ExtDirs[i].Range = sourceRange(n.ExtDirs[i].Range, *sm)
	}
}

// UTF16Col converts a byte column on the given source line to UTF-16 code
// units, matching LSP expectations.
func UTF16Col(line []byte, byteCol uint) uint {
	if byteCol > uint(len(line)) {
		byteCol = uint(len(line))
	}
	var units uint
	for i := 0; i < int(byteCol); i++ {
		c := line[i]
		if c >= 0x80 {
			// Decode the UTF-8 rune starting at i.
			if c&0xE0 == 0xC0 {
				i += 1
				units++
			} else if c&0xF0 == 0xE0 {
				i += 2
				units++
			} else if c&0xF8 == 0xF0 {
				i += 3
				units += 2 // astral plane -> surrogate pair
			}
			continue
		}
		units++
	}
	return units
}

// Lines exposes the source lines of the checked file.
func (r *FileResult) Lines() [][]byte {
	return r.contentLines
}

// Render formats the diagnostic in text form using UTF-16 columns computed
// against this file's source lines.
func (r *FileResult) Render(d Diag) string {
	var line []byte
	if int(d.StartLine) < len(r.contentLines) {
		line = r.contentLines[d.StartLine]
	}
	return r.File + ":" + itoa(d.StartLine+1) + ":" + itoa(UTF16Col(line, d.StartCol)+1) + ": " +
		d.Severity.String() + " " + d.Code + ": " + strings.TrimSpace(d.Message)
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}