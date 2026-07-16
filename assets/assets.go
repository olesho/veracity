// Package assets embeds every file the harness binary stamps into managed
// projects — language templates, project wiring, and the global setup frontend —
// plus the extractors it runs. Nothing here is ever committed into a managed
// project as source; templates render content and wiring renders declarative
// config that calls the installed `harness` binary.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:templates all:wiring all:global-frontend all:extractors
var files embed.FS

// FS returns the embedded asset filesystem (slash-separated paths rooted at the
// asset directory names, e.g. "templates/go/greeter.go").
func FS() fs.FS { return files }

// Read returns the bytes of an embedded asset by slash path.
func Read(name string) ([]byte, error) { return files.ReadFile(name) }

// ReadDir lists an embedded asset directory.
func ReadDir(name string) ([]fs.DirEntry, error) { return files.ReadDir(name) }
