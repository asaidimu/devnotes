// Package goextract extracts comment regions from Go source using go/parser.
package goextract

import (
	"go/parser"
	"go/token"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/normalize"
)

// Extract returns comment regions from a Go file, grouping contiguous line
// comments so a DevNote spanning several // lines becomes one region.
func Extract(filename string, src []byte) ([]normalize.Region, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var out []normalize.Region
	var cur *normalize.Region
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}

	for _, group := range f.Comments {
		for _, c := range group.List {
			pos := fset.Position(c.Slash)
			line := uint(pos.Line - 1)
			col := uint(pos.Column - 1)
			text := c.Text
			if strings.HasPrefix(text, "//") {
				if cur != nil && line == cur.StartLine+uint(len(strings.Split(cur.Raw, "\n"))-1)+1 && col == cur.StartCol {
					cur.Raw += "\n" + text
				} else {
					flush()
					cur = &normalize.Region{Raw: text, StartLine: line, StartCol: col, LineStyle: true}
				}
			} else {
				flush()
				out = append(out, normalize.Region{Raw: text, StartLine: line, StartCol: col, BlockStyle: true})
			}
		}
		flush()
	}
	return out, nil
}
