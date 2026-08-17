// Package tsextract extracts comment regions from TypeScript / JavaScript
// source without a full grammar. It lexes just enough to skip strings,
// template literals (including ${...} interpolation), and regex literals so
// that // and /* */ comments are located reliably.
package tsextract

type tokenClass int

const (
	tcStart tokenClass = iota
	tcWord            // identifier, keyword, number
	tcString          // '...', "...", `...`, regex literal
	tcClose           // ), ], }, ++, --
	tcOp              // operators and expression-opening punctuation
)

var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true,
	"of": true, "new": true, "delete": true, "void": true, "case": true,
	"do": true, "else": true, "yield": true, "await": true, "throw": true,
}

// Span is a located comment.
type Span struct {
	StartLine, StartCol, EndLine, EndCol uint
	Text                                  string
	LineStyle                             bool
}

type scanner struct {
	src   []byte
	i     int
	line  uint
	col   uint
	prev  tokenClass
	spans []Span
}

func newScanner(src []byte) *scanner {
	return &scanner{src: src, prev: tcStart}
}

func (s *scanner) peek() byte {
	if s.i < len(s.src) {
		return s.src[s.i]
	}
	return 0
}

func (s *scanner) advance() byte {
	if s.i >= len(s.src) {
		return 0
	}
	c := s.src[s.i]
	s.i++
	if c == '\n' {
		s.line++
		s.col = 0
	} else {
		s.col++
	}
	return c
}

func isSpace(c byte) bool      { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isHexDigit(c byte) bool   { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// skipWhitespaceAndComments consumes whitespace and records any comments.
func (s *scanner) skipWhitespaceAndComments() {
	for {
		for s.i < len(s.src) && isSpace(s.peek()) {
			s.advance()
		}
		if s.i+1 < len(s.src) && s.peek() == '/' {
			if s.src[s.i+1] == '/' {
				s.spans = append(s.spans, s.lineComment())
				continue
			}
			if s.src[s.i+1] == '*' {
				s.spans = append(s.spans, s.blockComment())
				continue
			}
		}
		return
	}
}

func (s *scanner) lineComment() Span {
	startLine, startCol := s.line, s.col
	start := s.i
	for s.i < len(s.src) && s.src[s.i] != '\n' {
		s.advance()
	}
	return Span{startLine, startCol, s.line, s.col, string(s.src[start:s.i]), true}
}

func (s *scanner) blockComment() Span {
	startLine, startCol := s.line, s.col
	start := s.i
	s.advance() // /
	s.advance() // *
	for s.i < len(s.src) {
		if s.peek() == '*' && s.i+1 < len(s.src) && s.src[s.i+1] == '/' {
			s.advance()
			s.advance()
			return Span{startLine, startCol, s.line, s.col, string(s.src[start:s.i]), false}
		}
		s.advance()
	}
	return Span{startLine, startCol, s.line, s.col, string(s.src[start:s.i]), false}
}

// Extract is defined in regions.go.

func (s *scanner) stringLit(q byte) {
	s.advance()
	for s.i < len(s.src) {
		c := s.peek()
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		if c == q {
			s.advance()
			break
		}
		s.advance()
	}
	s.prev = tcString
}

func (s *scanner) templateLit() {
	s.advance()
	s.prev = tcString
	depth := 1
	for s.i < len(s.src) {
		c := s.peek()
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		if c == '`' {
			s.advance()
			return
		}
		if c == '$' && s.i+1 < len(s.src) && s.src[s.i+1] == '{' {
			s.advance()
			s.advance()
			s.prev = tcStart
			s.lexExprBody(depth)
			continue
		}
		s.advance()
	}
}

// lexExprBody scans the code inside ${...}; depth is the brace nesting of the
// interpolation. It returns when the matching '}' is consumed.
func (s *scanner) lexExprBody(depth int) {
	for s.i < len(s.src) {
		s.skipWhitespaceAndComments()
		if s.i >= len(s.src) {
			return
		}
		c := s.peek()
		switch {
		case c == '{':
			depth++
			s.advance()
			s.prev = tcOp
		case c == '}':
			depth--
			s.advance()
			if depth == 0 {
				s.prev = tcString
				return
			}
			s.prev = tcClose
		case c == '\'' || c == '"':
			s.stringLit(c)
		case c == '`':
			s.templateLit()
			depth = 1
		case c == '/':
			s.slashToken()
		case isIdentStart(c):
			s.word()
		case isDigit(c):
			s.number()
		default:
			s.punct()
		}
	}
}

func (s *scanner) slashToken() {
	// '//' and '/*' are consumed earlier; here '/' is division or a regex.
	if s.prev == tcStart || s.prev == tcOp {
		s.regexLit()
	} else {
		s.advance()
		s.prev = tcOp
	}
}

func (s *scanner) regexLit() {
	s.advance() // opening '/'
	inClass := false
	for s.i < len(s.src) {
		c := s.peek()
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			s.advance()
			break
		} else if c == '\n' {
			// Unterminated regex: stop without consuming the newline.
			break
		}
		s.advance()
	}
	s.prev = tcString
}

func (s *scanner) number() {
	if s.peek() == '.' {
		s.advance()
	}
	if s.peek() == '0' && s.i+1 < len(s.src) && (s.src[s.i+1] == 'x' || s.src[s.i+1] == 'X') {
		s.advance()
		s.advance()
		for s.i < len(s.src) && (isHexDigit(s.peek()) || s.peek() == '_') {
			s.advance()
		}
		s.prev = tcWord
		return
	}
	for s.i < len(s.src) {
		c := s.peek()
		if isDigit(c) || c == '_' {
			s.advance()
			continue
		}
		if c == '.' {
			s.advance()
			continue
		}
		if c == 'e' || c == 'E' {
			s.advance()
			if s.peek() == '+' || s.peek() == '-' {
				s.advance()
			}
			continue
		}
		break
	}
	s.prev = tcWord
}

func (s *scanner) word() {
	start := s.i
	for s.i < len(s.src) && isIdentPart(s.peek()) {
		s.advance()
	}
	w := string(s.src[start:s.i])
	if regexKeywords[w] {
		s.prev = tcOp
	} else {
		s.prev = tcWord
	}
}

func (s *scanner) punct() {
	c := s.advance()
	switch c {
	case ')', ']', '}':
		s.prev = tcClose
	case '+', '-':
		// ++ / -- make the operand "complete" (division context).
		if s.peek() == c {
			s.advance()
			s.prev = tcClose
		} else {
			s.prev = tcOp
		}
	default:
		s.prev = tcOp
	}
}