// Package store is Quartzite's read-write corpus.
//
// A document here is a file, a folder with an entry point, or a zip archive.
// Folders and archives both satisfy fs.FS, so they serve through one code path
// and a bundled React app needs no more special handling than a note does.
package store

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bachittle/quartzite/internal/graph"
	"github.com/bachittle/quartzite/internal/model"
	"github.com/bachittle/quartzite/internal/parse"
)

// entryCandidates are the filenames tried, in order, when deciding what a
// bundle opens with.
var entryCandidates = []string{"index.html", "index.htm", "main.html", "app.html"}

const metaDir = ".quartzite"

var (
	// ErrNotFound is returned when no document matches an identifier.
	ErrNotFound = errors.New("document not found")
	// ErrExists is returned when creating a document whose path is taken.
	ErrExists = errors.New("document already exists")
	// ErrUnsafePath is returned when a path would escape the store root.
	ErrUnsafePath = errors.New("unsafe path")
)

// Store owns the read-write document tree.
type Store struct {
	Root string

	mu    sync.RWMutex
	docs  []*model.Doc
	byID  map[string]*model.Doc
	ids   map[string]string // relative path -> stable ID
	dirty bool
}

// Open prepares a store rooted at dir, creating it if absent.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, metaDir), 0o755); err != nil {
		return nil, err
	}
	s := &Store{Root: dir, byID: map[string]*model.Doc{}, ids: map[string]string{}}
	s.loadIDs()
	return s, nil
}

func (s *Store) idFile() string { return filepath.Join(s.Root, metaDir, "ids.json") }

func (s *Store) loadIDs() {
	b, err := os.ReadFile(s.idFile())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(b, &m) == nil && m != nil {
		s.ids = m
	}
}

// saveIDs persists path-to-ID bindings so a reference handed to an agent stays
// valid across restarts. Caller holds the lock.
func (s *Store) saveIDs() {
	if !s.dirty {
		return
	}
	b, err := json.MarshalIndent(s.ids, "", "  ")
	if err != nil {
		return
	}
	tmp := s.idFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, s.idFile())
		s.dirty = false
	}
}

func newID() string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("q%d", time.Now().UnixNano()%1e10)
	}
	return "q" + hex.EncodeToString(b[:])
}

// idFor returns the stable ID for a path, minting one on first sight.
// Caller holds the lock.
func (s *Store) idFor(rel string) string {
	if id, ok := s.ids[rel]; ok {
		return id
	}
	id := newID()
	s.ids[rel] = id
	s.dirty = true
	return id
}

// Scan rebuilds the in-memory index from disk.
func (s *Store) Scan() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var docs []*model.Doc
	live := map[string]bool{}

	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == metaDir {
				continue
			}
			childRel := path.Join(rel, name)
			childAbs := filepath.Join(dir, name)

			if e.IsDir() {
				// A directory holding an entry point is one document, not a
				// namespace. This is what makes "a folder of index.html + JS"
				// a first-class document rather than a pile of files.
				if entry := detectEntry(childAbs); entry != "" {
					if d := s.bundleDoc(childAbs, childRel, entry, model.KindFolder); d != nil {
						docs = append(docs, d)
						live[childRel] = true
					}
					continue
				}
				if err := walk(childAbs, childRel); err != nil {
					return err
				}
				continue
			}

			fi, err := e.Info()
			if err != nil {
				continue
			}
			if strings.EqualFold(filepath.Ext(name), ".zip") {
				if entry := detectZipEntry(childAbs); entry != "" {
					if d := s.bundleDoc(childAbs, childRel, entry, model.KindZip); d != nil {
						d.Size = fi.Size()
						docs = append(docs, d)
						live[childRel] = true
					}
					continue
				}
			}
			if d := s.fileDoc(childAbs, childRel, fi); d != nil {
				docs = append(docs, d)
				live[childRel] = true
			}
		}
		return nil
	}

	if err := walk(s.Root, ""); err != nil {
		return err
	}

	// Forget IDs for paths that no longer exist, so the map does not grow
	// without bound across a long-lived process.
	for p := range s.ids {
		if !live[p] {
			delete(s.ids, p)
			s.dirty = true
		}
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	s.docs = docs
	s.byID = make(map[string]*model.Doc, len(docs))
	for _, d := range docs {
		s.byID[d.ID] = d
	}
	s.saveIDs()
	return nil
}

// detectEntry picks the file a folder document opens with.
//
// The rule is deliberately strict: only a named entry point makes a directory
// a bundle. Inferring one from "the folder happens to contain a single HTML
// file" swallows directories that are meant to be namespaces — viewers/ being
// exactly that case.
func detectEntry(dir string) string {
	for _, c := range entryCandidates {
		if fi, err := os.Stat(filepath.Join(dir, c)); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

func detectZipEntry(p string) string {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return ""
	}
	defer zr.Close()
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	for _, c := range entryCandidates {
		if have[c] {
			return c
		}
	}
	return ""
}

func (s *Store) fileDoc(abs, rel string, fi os.FileInfo) *model.Doc {
	format := model.FormatOf(rel)
	d := &model.Doc{
		ID:       s.idFor(rel),
		Title:    model.TitleOf(rel),
		Kind:     model.KindFile,
		Source:   model.SourceStore,
		Path:     rel,
		Format:   format,
		Size:     fi.Size(),
		Modified: fi.ModTime().UTC(),
	}
	if model.IsText(format) && fi.Size() < 2<<20 {
		if b, err := os.ReadFile(abs); err == nil {
			meta, body := parse.Frontmatter(string(b))
			if t := parse.StringField(meta, "title"); t != "" {
				d.Title = t
			}
			d.Viewer = parse.StringField(meta, "viewer")
			scannable := parse.StripCode(body)
			tags := parse.ListField(meta, "tags", "tag")
			// Inline #tags only make sense in prose. In a stylesheet or a
			// script they are noise at best.
			if isProse(format) {
				tags = append(tags, graph.ExtractTags(scannable)...)
			}
			d.Tags = dedupe(tags)
			d.Links = graph.ExtractLinks(scannable)
			d.Excerpt = parse.Excerpt(body, 220)
		}
	}
	return d
}

func (s *Store) bundleDoc(abs, rel, entry string, kind model.Kind) *model.Doc {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	d := &model.Doc{
		ID:       s.idFor(rel),
		Title:    model.TitleOf(rel + "/x"), // title from the folder name itself
		Kind:     kind,
		Source:   model.SourceStore,
		Path:     rel,
		Format:   model.FormatOf(entry),
		Entry:    entry,
		Modified: fi.ModTime().UTC(),
	}
	if kind == model.KindFolder {
		d.Title = model.TitleOf(path.Base(rel) + ".x")
		d.Size = dirSize(abs)
	}

	// A bundle can carry a meta.json to override what the filesystem implies.
	if b, err := os.ReadFile(filepath.Join(abs, "meta.json")); err == nil && kind == model.KindFolder {
		var m struct {
			Title  string   `json:"title"`
			Tags   []string `json:"tags"`
			Format string   `json:"format"`
			Entry  string   `json:"entry"`
			Desc   string   `json:"description"`
			Viewer string   `json:"viewer"`
		}
		if json.Unmarshal(b, &m) == nil {
			if m.Title != "" {
				d.Title = m.Title
			}
			if m.Format != "" {
				d.Format = m.Format
			}
			if m.Entry != "" {
				d.Entry = m.Entry
			}
			if m.Desc != "" {
				d.Excerpt = m.Desc
			}
			if m.Viewer != "" {
				d.Viewer = m.Viewer
			}
			d.Tags = dedupe(m.Tags)
		}
	}

	if d.Excerpt == "" {
		if fsys, closer, err := s.bundleFS(d); err == nil {
			if f, err := fsys.Open(d.Entry); err == nil {
				b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
				f.Close()
				d.Excerpt = parse.Excerpt(string(b), 200)
				d.Links = graph.ExtractLinks(string(b))
			}
			if closer != nil {
				closer.Close()
			}
		}
	}
	return d
}

func dirSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// isProse reports whether a format is written for humans to read, as opposed
// to code that merely contains punctuation resembling markup.
func isProse(format string) bool {
	switch strings.ToLower(format) {
	case "md", "markdown", "txt":
		return true
	}
	return false
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

// Docs returns the current index.
func (s *Store) Docs() []*model.Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*model.Doc(nil), s.docs...)
}

// ByID looks up a single document.
func (s *Store) ByID(id string) (*model.Doc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.byID[id]
	return d, ok
}

// abs resolves a store-relative path, refusing anything that escapes the root.
func (s *Store) abs(rel string) (string, error) {
	clean := filepath.Clean(filepath.Join(s.Root, filepath.FromSlash(rel)))
	root := filepath.Clean(s.Root)
	if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return "", ErrUnsafePath
	}
	return clean, nil
}

// bundleFS exposes a bundle's contents as a filesystem. The returned closer is
// non-nil for archives and must be closed when serving finishes.
func (s *Store) bundleFS(d *model.Doc) (fs.FS, io.Closer, error) {
	abs, err := s.abs(d.Path)
	if err != nil {
		return nil, nil, err
	}
	switch d.Kind {
	case model.KindFolder:
		return os.DirFS(abs), nil, nil
	case model.KindZip:
		zr, err := zip.OpenReader(abs)
		if err != nil {
			return nil, nil, err
		}
		return zr, zr, nil
	}
	return nil, nil, fmt.Errorf("not a bundle")
}

// BundleFS is the exported form used by the HTTP layer.
func (s *Store) BundleFS(d *model.Doc) (fs.FS, io.Closer, error) { return s.bundleFS(d) }

// ReadFile returns a store document's raw bytes. For bundles it returns the
// entry file.
func (s *Store) ReadFile(d *model.Doc) ([]byte, error) {
	if d.Bundled() {
		fsys, closer, err := s.bundleFS(d)
		if err != nil {
			return nil, err
		}
		if closer != nil {
			defer closer.Close()
		}
		f, err := fsys.Open(d.Entry)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}
	abs, err := s.abs(d.Path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// CreateSpec describes a document to write.
type CreateSpec struct {
	Title   string            `json:"title"`
	Format  string            `json:"format"`
	Path    string            `json:"path"`
	Content string            `json:"content"`
	Files   map[string]string `json:"files"`
	Tags    []string          `json:"tags"`
}

// Create writes a new document. Supplying Files makes a folder bundle;
// supplying Content makes a single file.
func (s *Store) Create(spec CreateSpec) (*model.Doc, error) {
	if strings.TrimSpace(spec.Title) == "" && spec.Path == "" {
		return nil, errors.New("title or path required")
	}
	rel := spec.Path
	if rel == "" {
		rel = slugify(spec.Title)
		if len(spec.Files) == 0 {
			format := spec.Format
			if format == "" {
				format = "md"
			}
			rel += "." + format
		}
	}
	rel = path.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	abs, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err == nil {
		return nil, ErrExists
	}

	if len(spec.Files) > 0 {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
		for name, content := range spec.Files {
			if err := s.writeInto(rel, name, content); err != nil {
				return nil, err
			}
		}
		if spec.Title != "" || len(spec.Tags) > 0 {
			meta, _ := json.MarshalIndent(map[string]any{
				"title": spec.Title, "tags": spec.Tags,
			}, "", "  ")
			os.WriteFile(filepath.Join(abs, "meta.json"), meta, 0o644)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		body := spec.Content
		if spec.Title != "" && strings.EqualFold(spec.Format, "md") && !strings.HasPrefix(body, "---") {
			body = "---\ntitle: " + spec.Title + "\n---\n\n" + body
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}

	if err := s.Scan(); err != nil {
		return nil, err
	}
	for _, d := range s.Docs() {
		if d.Path == rel {
			return d, nil
		}
	}
	return nil, ErrNotFound
}

// writeInto writes one file inside a bundle, rejecting traversal in the name.
func (s *Store) writeInto(bundleRel, name, content string) error {
	target := path.Join(bundleRel, path.Clean("/"+name))
	abs, err := s.abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// Update replaces a document's content. Bundles update their entry file unless
// a specific file is named.
func (s *Store) Update(id, content, file string) (*model.Doc, error) {
	d, ok := s.ByID(id)
	if !ok {
		return nil, ErrNotFound
	}
	if d.Kind == model.KindZip {
		return nil, errors.New("zip documents are read-only; unpack to a folder to edit")
	}
	rel := d.Path
	if d.Kind == model.KindFolder {
		name := file
		if name == "" {
			name = d.Entry
		}
		if err := s.writeInto(rel, name, content); err != nil {
			return nil, err
		}
	} else {
		abs, err := s.abs(rel)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	if err := s.Scan(); err != nil {
		return nil, err
	}
	nd, ok := s.ByID(id)
	if !ok {
		return nil, ErrNotFound
	}
	return nd, nil
}

// Delete removes a document and its assets.
func (s *Store) Delete(id string) error {
	d, ok := s.ByID(id)
	if !ok {
		return ErrNotFound
	}
	abs, err := s.abs(d.Path)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	return s.Scan()
}

// Rename changes a document's title. The caller is responsible for rewriting
// inbound links, since only it can see both corpora.
func (s *Store) Rename(id, title string) (*model.Doc, error) {
	d, ok := s.ByID(id)
	if !ok {
		return nil, ErrNotFound
	}
	switch d.Kind {
	case model.KindFolder:
		abs, err := s.abs(d.Path)
		if err != nil {
			return nil, err
		}
		metaPath := filepath.Join(abs, "meta.json")
		m := map[string]any{}
		if b, err := os.ReadFile(metaPath); err == nil {
			json.Unmarshal(b, &m)
		}
		m["title"] = title
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(metaPath, b, 0o644); err != nil {
			return nil, err
		}
	case model.KindFile:
		abs, err := s.abs(d.Path)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		meta, body := parse.Frontmatter(string(b))
		if meta == nil {
			meta = map[string]any{}
		}
		meta["title"] = title
		var sb strings.Builder
		sb.WriteString("---\n")
		keys := make([]string, 0, len(meta))
		for k := range meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch v := meta[k].(type) {
			case []string:
				sb.WriteString(k + ": [" + strings.Join(v, ", ") + "]\n")
			default:
				sb.WriteString(fmt.Sprintf("%s: %v\n", k, v))
			}
		}
		sb.WriteString("---\n")
		sb.WriteString(strings.TrimLeft(body, "\n"))
		if err := os.WriteFile(abs, []byte(sb.String()), 0o644); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("cannot retitle this document kind")
	}
	if err := s.Scan(); err != nil {
		return nil, err
	}
	nd, _ := s.ByID(id)
	return nd, nil
}

// RewriteLinks replaces [[old]] with [[new]] across writable text documents,
// returning how many documents changed.
func (s *Store) RewriteLinks(oldTitle, newTitle string) int {
	if strings.EqualFold(oldTitle, newTitle) {
		return 0
	}
	changed := 0
	for _, d := range s.Docs() {
		if !model.IsText(d.Format) || d.Kind == model.KindZip {
			continue
		}
		b, err := s.ReadFile(d)
		if err != nil {
			continue
		}
		src := string(b)
		out := replaceWikilink(src, oldTitle, newTitle)
		if out == src {
			continue
		}
		if _, err := s.Update(d.ID, out, ""); err == nil {
			changed++
		}
	}
	return changed
}

// replaceWikilink swaps a link target while preserving aliases and anchors.
func replaceWikilink(src, oldTitle, newTitle string) string {
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(src[i:], "[[")
		if j < 0 {
			b.WriteString(src[i:])
			return b.String()
		}
		j += i
		k := strings.Index(src[j:], "]]")
		if k < 0 {
			b.WriteString(src[i:])
			return b.String()
		}
		k += j
		inner := src[j+2 : k]
		target, suffix := inner, ""
		if x := strings.IndexAny(inner, "|#"); x >= 0 {
			target, suffix = inner[:x], inner[x:]
		}
		b.WriteString(src[i:j])
		if strings.EqualFold(strings.TrimSpace(target), oldTitle) {
			b.WriteString("[[" + newTitle + suffix + "]]")
		} else {
			b.WriteString(src[j : k+2])
		}
		i = k + 2
	}
}

func slugify(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '/' :
			b.WriteRune('/')
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "untitled"
	}
	return out
}
