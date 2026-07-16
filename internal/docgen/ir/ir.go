// Package ir defines the language-agnostic intermediate representation the
// docgen extractors emit and the markdown/diagram renderers consume. It is
// deterministic (stable ordering, no timestamps) so equal source produces equal
// IR, which the freshness cache and golden tests depend on.
package ir

// SchemaVersion is the IR document version; it participates in the docgen cache
// key so a schema change invalidates stale caches.
const SchemaVersion = 1

// IR is the extracted model for one project.
type IR struct {
	SchemaVersion int      `json:"schemaVersion"`
	Subproject    string   `json:"subproject"`
	Language      string   `json:"language"`
	Modules       []Module `json:"modules"`
	Edges         []Edge   `json:"edges"`
}

// Module is a unit of code: a Go package, a Python package/dir, or a TS source
// directory.
type Module struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"` // project-root-relative
	// ContentHash is a deterministic digest of the module's source files. An
	// agent-written summary is reused as long as this is unchanged (the
	// "git-hash" equivalent that avoids re-summarizing unchanged code).
	ContentHash string      `json:"contentHash"`
	Files       []string    `json:"files"`
	DocComment  string      `json:"docComment,omitempty"`
	Exports     []Export    `json:"exports"`
	Interfaces  []Interface `json:"interfaces"`
}

// Export is an exported top-level declaration (function, type, const, var).
type Export struct {
	Kind       string `json:"kind"` // func|type|const|var
	Name       string `json:"name"`
	Signature  string `json:"signature"`
	DocComment string `json:"docComment,omitempty"`
	Line       int    `json:"line,omitempty"`
}

// Interface is a boundary contract (Go interface, Python Protocol/ABC, TS
// interface) with its method set.
type Interface struct {
	Name string `json:"name"`
	// ContentHash digests the interface's own declaration (name + doc + method
	// signatures) so its description survives edits elsewhere in the module.
	ContentHash string   `json:"contentHash"`
	DocComment  string   `json:"docComment,omitempty"`
	Methods     []Method `json:"methods"`
}

// Method is one method of an interface.
type Method struct {
	Signature  string `json:"signature"`
	DocComment string `json:"docComment,omitempty"`
}

// Edge is an intra-project dependency between modules, optionally labeled with
// the interface that mediates it.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via,omitempty"`
}
