// Package normalize implements SPEC 5 comment normalization.
//
// Host adapters hand it a raw comment region (including markers) and receive
// the normalized DevNotes content plus a SourceMap for mapping diagnostic
// ranges back to the host source.
package normalize

import "strings"

// Region is a raw comment region extracted by a host adapter.
type Region struct {
	// Raw is the exact source text of the comment. For grouped line
	// comments this is the lines joined with "\n".
	Raw string
	// StartLine/StartCol is the 0-based position of Raw's first line in
	// the host source.
	StartLine uint
	StartCol  uint
	// LineStyle indicates per-line markers (e.g. "//"); BlockStyle a
	// /* ... */ block. One of the two must be set.
	LineStyle  bool
	BlockStyle bool
}

// LineRef maps one normalized line back to the host source.
type LineRef struct {
	Line  uint // 0-based host source line
	Shift uint // host col = Shift + normalized col
}

// SourceMap maps normalized lines to host source positions.
type SourceMap struct {
	Lines []LineRef
}

// MapRange maps a normalized range to host source coordinates.
func (m *SourceMap) MapRange(startLine, startCol, endLine, endCol uint) (sl, sc, el, ec uint) {
	sl, sc = m.mapPoint(startLine, startCol)
	el, ec = m.mapPoint(endLine, endCol)
	return
}

func (m *SourceMap) mapPoint(line, col uint) (uint, uint) {
	if line >= uint(len(m.Lines)) {
		if len(m.Lines) == 0 {
			return 0, 0
		}
		ref := m.Lines[len(m.Lines)-1]
		return ref.Line, ref.Shift + col
	}
	ref := m.Lines[line]
	return ref.Line, ref.Shift + col
}

// Normalize converts a raw comment region into normalized content + a map.
func Normalize(r Region) (string, SourceMap) {
	if r.BlockStyle {
		return normalizeBlock(r)
	}
	return normalizeLines(r)
}

// stripLineMarker removes the marker plus one optional following space.
func stripLineMarker(line, marker string) string {
	if !strings.HasPrefix(line, marker) {
		return line
	}
	rest := line[len(marker):]
	if strings.HasPrefix(rest, " ") {
		rest = rest[1:]
	}
	return rest
}

func normalizeLines(r Region) (string, SourceMap) {
	marker := "//"
	// Sniff the marker from the first line (some adapters use # or --).
	for _, m := range []string{"//", "#", "--", ";", "*"} {
		if strings.HasPrefix(r.Raw, m) {
			marker = m
			break
		}
	}
	lines := strings.Split(r.Raw, "\n")
	var out []string
	m := SourceMap{}
	for i, ln := range lines {
		shift := uint(0)
		if strings.HasPrefix(ln, marker) {
			stripped := stripLineMarker(ln, marker)
			shift = uint(len(ln) - len(stripped))
			ln = stripped
		}
		out = append(out, ln)
		m.Lines = append(m.Lines, LineRef{Line: r.StartLine + uint(i), Shift: shift})
	}
	return strings.Join(out, "\n"), m
}

func normalizeBlock(r Region) (string, SourceMap) {
	lines := strings.Split(r.Raw, "\n")

	// Single-line block: "/* @note ... */".
	if len(lines) == 1 {
		line := lines[0]
		line = strings.TrimPrefix(line, "/*")
		line = strings.TrimSuffix(line, "*/")
		if strings.HasPrefix(line, " ") {
			line = line[1:]
		}
		if strings.HasSuffix(line, " ") {
			line = strings.TrimSuffix(line, " ")
		}
		return line, SourceMap{Lines: []LineRef{{Line: r.StartLine, Shift: 2}}}
	}

	// Interior lines between the delimiters.
	interior := lines[1 : len(lines)-1]

	// Compute the common decorative marker (leading ws + '*') shared by all
	// non-blank interior lines, or "" if none.
	marker := decorativeMarker(interior)

	var out []string
	m := SourceMap{}
	for i, ln := range interior {
		trimmed := strings.TrimRight(ln, " \t")
		blank := trimmed == ""
		shift := uint(0)
		stripped := ln
		if !blank && marker != "" && strings.HasPrefix(ln, marker) {
			shift = uint(len(marker))
			stripped = ln[len(marker):]
			if strings.HasPrefix(stripped, " ") {
				shift++
				stripped = stripped[1:]
			}
		}
		out = append(out, stripped)
		// Interior line i sits at source line StartLine+1+i.
		m.Lines = append(m.Lines, LineRef{Line: r.StartLine + 1 + uint(i), Shift: r.StartCol + shift})
	}
	return strings.Join(out, "\n"), m
}

// decorativeMarker returns the leading-whitespace-plus-'*' marker shared by
// all non-blank interior lines, or "" if lines do not share a marker column.
func decorativeMarker(lines []string) string {
	var marker string
	for _, ln := range lines {
		if strings.TrimRight(ln, " \t") == "" {
			continue
		}
		p := markerOfLine(ln)
		if p == "" {
			return ""
		}
		if marker == "" {
			marker = p
			continue
		}
		if p != marker {
			return ""
		}
	}
	return marker
}

func markerOfLine(ln string) string {
	ws := ""
	rest := ln
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
		ws += string(rest[0])
		rest = rest[1:]
	}
	if rest == "" {
		return ""
	}
	if rest[0] == '*' {
		return ws + "*"
	}
	return ""
}
