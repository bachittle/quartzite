// Package model holds the types shared across Quartzite's subsystems.
//
// A document is deliberately not "a file". It is an entry point plus whatever
// assets it needs, which is what lets an agent write a React app and a plain
// note into the same graph.
package model

import (
	"path"
	"strings"
	"time"
)

// Kind is the physical shape of a document on disk.
type Kind string

const (
	// KindFile is a single self-contained file: notes.md, chart.html.
	KindFile Kind = "file"
	// KindFolder is a directory with an entry point, served with its assets.
	KindFolder Kind = "folder"
	// KindZip is an archive served directly from the compressed bytes.
	KindZip Kind = "zip"
)

// Source is which of the two corpora a document lives in.
type Source string

const (
	// SourceStore is Quartzite's read-write store.
	SourceStore Source = "store"
	// SourceVault is the vault of existing notes, indexed read-only and never written.
	SourceVault Source = "vault"
)

// Doc is one node in the graph.
type Doc struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   Kind   `json:"kind"`
	Source Source `json:"source"`

	// Path is relative to the source root, always slash-separated.
	Path string `json:"path"`
	// Format drives viewer selection: "md", "html", "fbp", ...
	Format string `json:"format"`
	// Viewer is a per-document override naming a viewer variant, from a
	// `viewer:` frontmatter key. A document choosing its own presentation is
	// the escape hatch that lets it be a thing rather than a row in a list.
	Viewer string `json:"viewer,omitempty"`
	// Entry is the file served first for folder and zip documents.
	Entry string `json:"entry,omitempty"`

	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`

	// Archived hides a document from default views. Held as Quartzite state
	// beside the store, never written into the document.
	Archived bool `json:"archived,omitempty"`

	// Links are the raw [[wikilink]] targets found in this document, by title.
	Links []string `json:"links,omitempty"`
	Tags  []string `json:"tags,omitempty"`

	// Excerpt is a short plain-text preview, populated for text formats.
	Excerpt string `json:"excerpt,omitempty"`
}

// Bundled reports whether the document carries assets alongside its entry point.
func (d *Doc) Bundled() bool { return d.Kind == KindFolder || d.Kind == KindZip }

// Writable reports whether this document may be modified through the API.
func (d *Doc) Writable() bool { return d.Source == SourceStore }

// TextFormats are the formats Quartzite will read as text for indexing,
// excerpting and link extraction. Anything else is treated as opaque bytes.
var TextFormats = map[string]bool{
	"md": true, "markdown": true, "txt": true, "html": true, "htm": true,
	"json": true, "js": true, "css": true, "csv": true, "yaml": true, "yml": true,
	"svg": true, "go": true, "py": true, "sh": true, "toml": true,
}

// IsText reports whether a format should be handled as text.
func IsText(format string) bool { return TextFormats[strings.ToLower(format)] }

// FormatOf derives a format key from a filename.
func FormatOf(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	if ext == "" {
		return "bin"
	}
	return ext
}

// TitleOf derives a human title from a path when no better source exists.
func TitleOf(p string) string {
	base := path.Base(p)
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	return strings.TrimSpace(base)
}

// Stats is a point-in-time summary of the index, surfaced on the dashboard.
type Stats struct {
	Docs        int            `json:"docs"`
	Archived    int            `json:"archived"`
	StoreDocs   int            `json:"storeDocs"`
	VaultDocs   int            `json:"vaultDocs"`
	Links       int            `json:"links"`
	Broken      int            `json:"broken"`
	ByKind      map[string]int `json:"byKind"`
	ByFormat    map[string]int `json:"byFormat"`
	Bytes       int64          `json:"bytes"`
	IndexedAt   time.Time      `json:"indexedAt"`
	IndexTimeMS float64        `json:"indexTimeMs"`
}
