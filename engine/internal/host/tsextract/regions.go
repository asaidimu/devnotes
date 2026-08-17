// Package tsextract extracts comment regions from TypeScript / JavaScript
// source without a full grammar. It lexes just enough to skip strings,
// template literals (including ${...} interpolation), and regex literals so
// that // and /* */ comments are located reliably.
package tsextract

import (
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/normalize"
)

// Extract returns comment spans in source order.
func Extract(src []byte) []Span {
	s := newScanner(src)
	for s.i < len(s.src) {
		s.skipWhitespaceAndComments()
		if s.i >= len(s.src) {
			break
		}
		c := s.peek()
		switch {
		case c == '\'' || c == '"':
			s.stringLit(c)
		case c == '`':
			s.templateLit()
		case c == '/':
			s.slashToken()
		case c == '.' && s.i+1 < len(s.src) && isDigit(s.src[s.i+1]):
			s.number()
		case isDigit(c):
			s.number()
		case isIdentStart(c):
			s.word()
		default:
			s.punct()
		}
	}
	return s.spans
}

// Regions converts spans into comment regions, grouping contiguous line
// comments so a DevNote spanning several // lines becomes one region.
func Regions(src []byte) []normalize.Region {
	spans := Extract(src)
	var out []normalize.Region
	var cur *normalize.Region
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, sp := range spans {
		if sp.LineStyle {
			if cur != nil && sp.StartLine == cur.StartLine+uint(len(strings.Split(cur.Raw, "\n"))-1)+1 && sp.StartCol == cur.StartCol {
				cur.Raw += "\n" + sp.Text
			} else {
				flush()
				r := normalize.Region{Raw: sp.Text, StartLine: sp.StartLine, StartCol: sp.StartCol, LineStyle: true}
				cur = &r
			}
		} else {
			flush()
			out = append(out, normalize.Region{Raw: sp.Text, StartLine: sp.StartLine, StartCol: sp.StartCol, BlockStyle: true})
		}
	}
	flush()
	return out
}