// Package cst wraps the tree-sitter devnotes parser for the engine.
package cst

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	devnotes "github.com/asaidimu/tree-sitter-devnotes/bindings/go"
)

// Parse parses normalized DevNotes content and returns the tree.
// The caller must Close the returned tree.
func Parse(src []byte) (*sitter.Tree, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitter.NewLanguage(devnotes.Language()))
	return parser.Parse(src, nil), nil
}

// Point is a position in the normalized content (0-based).
type Point struct {
	Row    uint
	Column uint
}

// Range is a span in the normalized content (0-based, end exclusive).
type Range struct {
	Start Point
	End   Point
}

// Children returns the named children of n, skipping anonymous nodes.
func Children(n *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.IsNamed() {
			out = append(out, c)
		}
	}
	return out
}

// NodeText returns the source text covered by n.
func NodeText(n *sitter.Node, src []byte) string {
	s, e := n.StartByte(), n.EndByte()
	if s > uint(len(src)) || e > uint(len(src)) || s > e {
		return ""
	}
	return string(src[s:e])
}

// HasMissingChild reports whether any descendant of n is a MISSING node.
func HasMissingChild(n *sitter.Node) bool {
	found := false
	walk(n, func(c *sitter.Node) bool {
		if c.IsMissing() {
			found = true
			return false
		}
		return true
	})
	return found
}

// HasError reports whether n or any descendant carries a parse error.
func HasError(n *sitter.Node) bool {
	return n.HasError()
}

func walk(n *sitter.Node, visit func(*sitter.Node) bool) {
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c != nil {
			walk(c, visit)
		}
	}
}
