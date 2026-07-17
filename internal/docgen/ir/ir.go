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
// interface) with its method set and the entities on either side of it: the
// modules that depend on it and the concrete types that implement it.
type Interface struct {
	Name string `json:"name"`
	// ContentHash digests the interface's own declaration (name + doc + method
	// signatures) so its description survives edits elsewhere in the module.
	ContentHash string   `json:"contentHash"`
	DocComment  string   `json:"docComment,omitempty"`
	Methods     []Method `json:"methods"`
	// Implementers are the concrete types across the project that satisfy this
	// interface (structural method-set match). Consumers are the module ids that
	// depend on it (reference it without implementing it).
	Implementers []Implementer `json:"implementers,omitempty"`
	Consumers    []string      `json:"consumers,omitempty"`
}

// Implementer is a concrete type that satisfies an interface.
type Implementer struct {
	Module string `json:"module"` // module id of the implementing type
	Type   string `json:"type"`   // concrete type name
}

// Method is one method of an interface.
type Method struct {
	Signature  string `json:"signature"`
	DocComment string `json:"docComment,omitempty"`
}

// Edge is an intra-project dependency between modules. Rel is the strict
// relationship label: "implements" when a type in From satisfies an interface
// defined in To, otherwise "depends on".
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
}

// Relationship labels used on edges.
const (
	RelImplements = "implements"
	RelDependsOn  = "depends on"
)
