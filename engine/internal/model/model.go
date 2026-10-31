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

	// Token-level source ranges (same coordinate space as Range). Zero
	// values mean "not captured"; consumers must fall back to Range.
	IDRange       Range
	CategoryRange Range
	TitleRange    Range
	HeaderRange   Range // the whole header_line node
	// TagRanges is parallel to Tags. AuthorLocs is parallel to Authors and
	// covers the author value token. DirectiveRanges holds every
	// directive_line node in source order.
	TagRanges       []Range
	AuthorLocs      []Range
	DirectiveRanges []Range
}

// ParseDiag reports a note that the grammar could not parse cleanly.
type ParseDiag struct {
	Code    string
	Message string
	Range   Range
}

const malformedHeaderMsg = "This note could not be parsed. A note starts with a header line " +
	"\"@note #id category [status] [priority] [timestamp] [#tags] : Title\", followed by optional " +
	"@author/@see directives, a blank line, and the body."

func r(n *sitter.Node) Range {
	sp := n.StartPosition()
	ep := n.EndPosition()
	return Range{uint(sp.Row), uint(sp.Column), uint(ep.Row), uint(ep.Column)}
}

// Build parses normalized content and returns the notes in source order.
func Build(src []byte) ([]Note, error) {
	notes, _, err := BuildWithDiags(src)
	return notes, err
}

// BuildWithDiags parses normalized content and additionally reports every
// region the grammar could not interpret as a note (INVALID_HEADER). Notes
// with recoverable errors are still returned so downstream tooling can
// offer fixes; the diagnostics make the breakage visible instead of
// silently dropping the text.
func BuildWithDiags(src []byte) ([]Note, []ParseDiag, error) {
	tree, err := cst.Parse(src)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	var notes []Note
	var diags []ParseDiag
	for _, blk := range cst.Children(root) {
		switch blk.Kind() {
		case "note_block":
			if blk.HasError() {
				diags = append(diags, ParseDiag{
					Code:    "INVALID_HEADER",
					Message: malformedHeaderMsg,
					Range:   errRange(blk),
				})
			}
			notes = append(notes, buildNote(src, blk))
		case "ERROR":
			diags = append(diags, ParseDiag{
				Code:    "INVALID_HEADER",
				Message: malformedHeaderMsg,
				Range:   r(blk),
			})
		}
	}
	return notes, diags, nil
}

// errRange narrows a block-level error to the first erroneous descendant so
// the squiggle lands on the broken tokens rather than the whole note.
func errRange(blk *sitter.Node) Range {
	var found Range
	ok := false
	var walkErr func(n *sitter.Node) *sitter.Node
	walkErr = func(n *sitter.Node) *sitter.Node {
		if n.IsError() {
			return n
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			if hit := walkErr(c); hit != nil {
				return hit
			}
		}
		return nil
	}
	if hit := walkErr(blk); hit != nil {
		found, ok = r(hit), true
	}
	if !ok {
		found = r(blk)
	}
	return found
}

func buildNote(src []byte, blk *sitter.Node) Note {
	var n Note
	n.Range = r(blk)

	var bodyLines []string
	for _, child := range cst.Children(blk) {
		switch child.Kind() {
		case "header_line":
			n.HeaderRange = r(child)
			buildHeader(src, child, &n)
		case "directive_line":
			n.DirectiveRanges = append(n.DirectiveRanges, r(child))
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
			n.IDRange = r(child)
		case "category":
			n.Category = cst.NodeText(child, src)
			n.CategoryRange = r(child)
		case "header_field":
			buildField(src, child, n)
		case "title":
			n.Title = cst.NodeText(child, src)
			n.TitleRange = r(child)
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
			n.TagRanges = nil
			for _, tag := range cst.Children(child) {
				if tag.Kind() == "tag_name" {
					n.Tags = append(n.Tags, cst.NodeText(tag, src))
					n.TagRanges = append(n.TagRanges, r(tag))
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
				n.AuthorLocs = append(n.AuthorLocs, r(av))
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
