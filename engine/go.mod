module github.com/asaidimu/devnotes/engine

go 1.23

replace github.com/asaidimu/tree-sitter-devnotes/bindings/go => ../bindings/go

require (
	github.com/asaidimu/tree-sitter-devnotes/bindings/go v0.0.0-00010101000000-000000000000
	github.com/tree-sitter/go-tree-sitter v0.25.0
)

require (
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/tree-sitter/tree-sitter-javascript v0.23.1 // indirect
	github.com/tree-sitter/tree-sitter-typescript v0.7.0 // indirect
)
