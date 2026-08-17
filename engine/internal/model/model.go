// Package model maps a devnotes CST into the SPEC 30 DevNote model.
package model

import (
	"fmt"
	"strings"

	"github.com/asaidimu/devnotes/engine/internal/cst"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Range is a span in the normalized content (0-based, end exclusive).
type Range struct {
	StartLine uint
	StartCol  uint
	EndLine   uint
	EndCol    uint
}

// HeaderField is a parsed header metadata field.
type HeaderField struct {
	Kind  string // status | priority | timestamp | tags | extension
	Value string
	Range Range
}

// Reference is a parsed @see target.
type Reference struct {
	Kind  string // id | url | other
	Value string
	Range Range
}

// Directive is a preserved unknown extension directive.
type Directive struct {
	Name     string
	Value    string
	HasValue bool
	Range    Range
}

// Note is a single DevNote (SPEC 30).
type Note struct {
	ID        string
	Category  string
	Status    string
	HasStatus bool
	Priority  string
	HasPri    bool
	Timestamp string
	HasTs     bool
	Tags      []string
	Ext       []HeaderField
	Title     string
	NoTitle   bool
	Authors   []string
	Refs      []Reference
	ExtDirs   []Directive
	Body      string
	HasBody   bool
	Range     Range
	// Fields is every header_field in source order, used for duplicate detection.
	Fields []HeaderField
}

func r(n *sitter.Node) Range {
	sp := n.StartPosition()
	ep := n.EndPosition()
	return Range{uint(sp.Row), uint(sp.Column), uint(ep.Row), uint(ep.Column)}
}

// Build parses normalized content and returns the notes in source order.
func Build(src []byte) ([]Note, error) {
	tree, err := cst.Parse(src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return buildTree(src, tree.RootNode()), nil
}

func buildTree(src []byte, root *sitter.Node) []Note {
	var notes []Note
	for _, blk := range cst.Children(root) {
		if blk.Kind() != "note_block" {
			continue
		}
		notes = append(notes, buildNote(src, blk))
	}
	return notes
}

func buildNote(src []byte, blk *sitter.Node) Note {
	var n Note
	n.Range = r(blk)

	var bodyLines []string
	for _, child := range cst.Children(blk) {
		switch child.Kind() {
		case "header_line":
			buildHeader(src, child, &n)
		case "directive_line":
			buildDirective(src, child, &n)
		case "body_line":
			bodyLines = append(bodyLines, bodyLineText(src, child))
		}
	}
	if len(bodyLines) > 0 {
		n.Body = strings.Join(bodyLines, "\n")
		n.HasBody = true
	}
	return n
}

func buildHeader(src []byte, header *sitter.Node, n *Note) {
	for _, child := range cst.Children(header) {
		switch child.Kind() {
		case "id":
			n.ID = cst.NodeText(child, src)
		case "category":
			n.Category = cst.NodeText(child, src)
		case "header_field":
			buildField(src, child, n)
		case "title":
			n.Title = cst.NodeText(child, src)
			n.NoTitle = child.HasError()
		}
	}
}

func buildField(src []byte, field *sitter.Node, n *Note) {
	for _, child := range cst.Children(field) {
		text := cst.NodeText(child, src)
		switch child.Kind() {
		case "status":
			n.Status, n.HasStatus = text, true
			n.Fields = append(n.Fields, HeaderField{Kind: "status", Value: text, Range: r(child)})
		case "priority":
			n.Priority, n.HasPri = text, true
			n.Fields = append(n.Fields, HeaderField{Kind: "priority", Value: text, Range: r(child)})
		case "timestamp":
			n.Timestamp, n.HasTs = text, true
			n.Fields = append(n.Fields, HeaderField{Kind: "timestamp", Value: text, Range: r(child)})
		case "extension_field":
			n.Ext = append(n.Ext, HeaderField{Kind: "extension", Value: text, Range: r(child)})
		case "tags":
			n.Tags = nil
			for _, tag := range cst.Children(child) {
				if tag.Kind() == "tag_name" {
					n.Tags = append(n.Tags, cst.NodeText(tag, src))
				}
			}
			n.Fields = append(n.Fields, HeaderField{Kind: "tags", Value: strings.Join(n.Tags, ","), Range: r(child)})
		}
	}
}

func buildDirective(src []byte, dl *sitter.Node, n *Note) {
	for _, child := range cst.Children(dl) {
		switch child.Kind() {
		case "author_directive":
			if av := namedChild(child, "author_value"); av != nil {
				n.Authors = append(n.Authors, cst.NodeText(av, src))
			}
		case "see_directive":
			if ref := namedChild(child, "reference"); ref != nil {
				n.Refs = append(n.Refs, buildReference(src, ref))
			}
		case "extension_directive":
			var d Directive
			d.Range = r(child)
			if name := namedChild(child, "directive_name"); name != nil {
				d.Name = cst.NodeText(name, src)
			}
			if val := namedChild(child, "directive_value"); val != nil {
				d.Value, d.HasValue = cst.NodeText(val, src), true
			}
			n.ExtDirs = append(n.ExtDirs, d)
		}
	}
}

func buildReference(src []byte, ref *sitter.Node) Reference {
	var out Reference
	out.Range = r(ref)
	if len(cst.Children(ref)) > 0 {
		c := cst.Children(ref)[0]
		out.Value = cst.NodeText(c, src)
		switch c.Kind() {
		case "id":
			out.Kind = "id"
		case "url":
			out.Kind = "url"
		default:
			out.Kind = "other"
		}
		return out
	}
	out.Value = cst.NodeText(ref, src)
	out.Kind = "other"
	return out
}

func bodyLineText(src []byte, bl *sitter.Node) string {
	if bt := namedChild(bl, "body_text"); bt != nil {
		return cst.NodeText(bt, src)
	}
	// Bare "@note" line (SPEC 24 escaping is handled upstream).
	if trimmed := strings.TrimRight(cst.NodeText(bl, src), "\r\n"); trimmed == "@note" {
		return "@note"
	}
	return "" // blank line
}

func namedChild(n *sitter.Node, typ string) *sitter.Node {
	for _, c := range cst.Children(n) {
		if c.Kind() == typ {
			return c
		}
	}
	return nil
}

// IDOf is a helper for CLI output.
func (n Note) String() string {
	return fmt.Sprintf("#%s [%s] %s", n.ID, n.Category, n.Title)
}
