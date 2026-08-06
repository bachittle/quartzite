// Package vault indexes the Obsidian vault.
//
// This corpus is strictly read-only. Nothing in Quartzite writes here, which is
// the whole reason agents can be given free rein over the store: the 748 notes
// that already exist cannot be damaged by a bad generation.
package vault

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bachittle/quartzite/internal/graph"
	"github.com/bachittle/quartzite/internal/model"
	"github.com/bachittle/quartzite/internal/parse"
)

// skipDirs are directories that hold machine state rather than notes.
var skipDirs = map[string]bool{
	".git": true, ".obsidian": true, ".trash": true, "node_modules": true,
}

// maxRead caps how much of a file is read for excerpting and link extraction.
// Vault notes are prose; a note large enough to exceed this is an outlier whose
// tail contributes nothing to search.
const maxRead = 512 << 10

// Index is a read-only snapshot of the vault.
type Index struct {
	Root string
	Docs []*model.Doc
}

// ID derives a stable identifier from a vault-relative path. Vault documents
// are never renamed by Quartzite, so deriving rather than persisting is safe
// and keeps the read-only corpus free of sidecar state.
func ID(rel string) string {
	sum := sha1.Sum([]byte("vault:" + rel))
	return "v" + hex.EncodeToString(sum[:])[:10]
}

// Scan walks the vault and builds an index. A missing vault is not an error:
// Quartzite runs fine with the store alone.
func Scan(root string) (*Index, error) {
	idx := &Index{Root: root}
	if root == "" {
		return idx, nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return idx, nil
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner should not abort the walk
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] || (strings.HasPrefix(name, ".") && p != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".markdown" && ext != ".txt" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		doc := buildDoc(p, rel, fi)
		if doc != nil {
			idx.Docs = append(idx.Docs, doc)
		}
		return nil
	})
	if err != nil {
		return idx, err
	}
	sort.Slice(idx.Docs, func(i, j int) bool { return idx.Docs[i].Path < idx.Docs[j].Path })
	return idx, nil
}

func buildDoc(abs, rel string, fi os.FileInfo) *model.Doc {
	body := ""
	if fi.Size() <= maxRead {
		if b, err := os.ReadFile(abs); err == nil {
			body = string(b)
		}
	} else if f, err := os.Open(abs); err == nil {
		buf := make([]byte, maxRead)
		n, _ := f.Read(buf)
		body = string(buf[:n])
		f.Close()
	}

	meta, content := parse.Frontmatter(body)
	title := parse.StringField(meta, "title")
	if title == "" {
		title = model.TitleOf(rel)
	}
	scannable := parse.StripCode(content)
	tags := parse.ListField(meta, "tags", "tag")
	tags = append(tags, graph.ExtractTags(scannable)...)

	return &model.Doc{
		ID:       ID(rel),
		Title:    title,
		Viewer:   parse.StringField(meta, "viewer"),
		Kind:     model.KindFile,
		Source:   model.SourceVault,
		Path:     rel,
		Format:   model.FormatOf(rel),
		Size:     fi.Size(),
		Modified: fi.ModTime().UTC(),
		Links:    graph.ExtractLinks(scannable),
		Tags:     dedupe(tags),
		Excerpt:  parse.Excerpt(content, 220),
	}
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Read returns the raw bytes of a vault document, refusing any path that
// escapes the vault root.
func (ix *Index) Read(rel string) ([]byte, error) {
	clean := filepath.Clean(filepath.Join(ix.Root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(clean, filepath.Clean(ix.Root)+string(os.PathSeparator)) {
		return nil, os.ErrPermission
	}
	return os.ReadFile(clean)
}

// Stat reports when the vault was last modified, used to decide whether a
// rescan is worthwhile.
func (ix *Index) Stat() time.Time {
	var newest time.Time
	for _, d := range ix.Docs {
		if d.Modified.After(newest) {
			newest = d.Modified
		}
	}
	return newest
}
