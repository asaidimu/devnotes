package tree_sitter_devnotes

// #cgo CFLAGS: -std=c11 -fPIC -I../../src
// #include "../../src/parser.c"
import "C"

import "unsafe"

// Get the tree-sitter Language for this grammar.
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_devnotes())
}
