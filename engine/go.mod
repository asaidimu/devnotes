module github.com/asaidimu/devnotes/engine

go 1.23

// Local dev: point the require below at the in-repo binding module.
// Downstream consumers ignore replace and resolve the tagged subdirectory
// module github.com/asaidimu/devnotes/bindings/go from the same repo.
replace github.com/asaidimu/devnotes/bindings/go => ../bindings/go

require (
	github.com/asaidimu/devnotes/bindings/go v0.1.0
	github.com/tree-sitter/go-tree-sitter v0.25.0
)

require github.com/mattn/go-pointer v0.0.1 // indirect
