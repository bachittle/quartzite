package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bachittle/quartzite/internal/graph"
	"github.com/bachittle/quartzite/internal/model"
	"github.com/bachittle/quartzite/internal/store"
)

// Routes builds the complete HTTP surface. Go's pattern-matching mux covers
// everything here, so there is no router dependency.
func (s *Service) Routes(webDir string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/viewers", s.handleViewers)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/reindex", s.handleReindex)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	mux.HandleFunc("GET /api/docs", s.handleListDocs)
	mux.HandleFunc("POST /api/docs", s.handleCreateDoc)
	mux.HandleFunc("GET /api/docs/{id}", s.handleGetDoc)
	mux.HandleFunc("PUT /api/docs/{id}", s.handleUpdateDoc)
	mux.HandleFunc("PATCH /api/docs/{id}", s.handlePatchDoc)
	mux.HandleFunc("DELETE /api/docs/{id}", s.handleDeleteDoc)
	mux.HandleFunc("GET /api/docs/{id}/raw", s.handleRawDoc)
	mux.HandleFunc("GET /api/docs/{id}/backlinks", s.handleBacklinks)

	// Bundle serving. The trailing wildcard is what lets a folder document's
	// relative <script src> and fetch() resolve exactly as they would on disk.
	mux.HandleFunc("GET /d/{id}", s.handleServeBundleRoot)
	mux.HandleFunc("GET /d/{id}/", s.handleServeBundle)

	// Registered without a method constraint on purpose. "GET /" would restrict
	// methods while "/api/" restricts path, and Go's mux refuses that pair as
	// ambiguous — neither pattern dominates the other.
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	return s.Metrics.Middleware(noCache(mux))
}

// noCache disables caching everywhere.
//
// Documents change under the browser constantly — that is the point of an
// agent-first store — and a stale bundle or viewer is indistinguishable from a
// bug. At sub-millisecond serving over loopback, caching buys nothing worth
// that confusion.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "ok",
		"docs":   s.Stats().Docs,
		"time":   time.Now().UTC(),
	})
}

func (s *Service) handleMetrics(w http.ResponseWriter, r *http.Request) {
	snap := s.Metrics.Snapshot()
	writeJSON(w, 200, map[string]any{
		"process":   snap,
		"index":     s.Stats(),
		"listeners": s.Hub.Count(),
	})
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Stats())
}

func (s *Service) handleViewers(w http.ResponseWriter, r *http.Request) {
	v := s.Viewers()
	if v == nil {
		v = []ViewerInfo{}
	}
	writeJSON(w, 200, v)
}

func (s *Service) handleReindex(w http.ResponseWriter, r *http.Request) {
	if err := s.Reindex(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Hub.Publish(Event{Type: "index", Data: s.Stats()})
	writeJSON(w, 200, s.Stats())
}

func (s *Service) handleListDocs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source := q.Get("source")
	kind := q.Get("kind")
	format := q.Get("format")
	// archived: "" hides them (the default), "true" shows only them,
	// "all" shows everything.
	archived := q.Get("archived")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}

	docs := s.Docs()
	out := make([]*model.Doc, 0, len(docs))
	for _, d := range docs {
		switch archived {
		case "true", "only":
			if !d.Archived {
				continue
			}
		case "all":
			// no filter
		default:
			if d.Archived {
				continue
			}
		}
		if source != "" && string(d.Source) != source {
			continue
		}
		if kind != "" && string(d.Kind) != kind {
			continue
		}
		if format != "" && !strings.EqualFold(d.Format, format) {
			continue
		}
		out = append(out, d)
	}
	total := len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, 200, map[string]any{"total": total, "count": len(out), "docs": out})
}

func (s *Service) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Doc(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "no such document")
		return
	}
	g := s.Graph()
	resp := map[string]any{
		"doc":       d,
		"backlinks": g.Backlinks(d.ID),
		"broken":    g.Broken(d.ID),
		"writable":  d.Writable(),
		"bundled":   d.Bundled(),
		"serveUrl":  "/d/" + d.ID + "/",
	}
	// The caller may state a preferred variant; the document's own declaration
	// still wins over it.
	if v, ok := s.ViewerFor(d, r.URL.Query().Get("viewer")); ok {
		resp["viewer"] = v
	}
	resp["viewers"] = s.ViewersFor(d.Format)
	// Resolved outbound links, so the UI can render them as navigation.
	var links []map[string]any
	for _, t := range d.Links {
		if dst, ok := g.Resolve(t); ok {
			links = append(links, map[string]any{"title": t, "id": dst.ID, "resolved": true})
		} else {
			links = append(links, map[string]any{"title": t, "resolved": false})
		}
	}
	resp["links"] = links

	if model.IsText(d.Format) && d.Size < 2<<20 {
		if b, err := s.ReadDoc(d); err == nil {
			resp["content"] = string(b)
		}
	}
	writeJSON(w, 200, resp)
}

func (s *Service) handleRawDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Doc(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "no such document")
		return
	}
	b, err := s.ReadDoc(d)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ct := contentTypeFor(d.Format)
	w.Header().Set("Content-Type", ct)
	w.Write(b)
}

func (s *Service) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Doc(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "no such document")
		return
	}
	refs := s.Graph().Backlinks(d.ID)
	if refs == nil {
		refs = []graph.Ref{} // keep JSON as [] rather than null
	}
	writeJSON(w, 200, map[string]any{"id": d.ID, "backlinks": refs})
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 40
	}
	hits := s.Search(q, limit)
	if hits == nil {
		hits = []Hit{}
	}
	writeJSON(w, 200, map[string]any{"query": q, "count": len(hits), "hits": hits})
}

func (s *Service) handleCreateDoc(w http.ResponseWriter, r *http.Request) {
	var spec store.CreateSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&spec); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	d, err := s.Store.Create(spec)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrExists):
			writeErr(w, 409, "a document already exists at that path")
		case errors.Is(err, store.ErrUnsafePath):
			writeErr(w, 400, "path escapes the store root")
		default:
			writeErr(w, 400, err.Error())
		}
		return
	}
	s.Reindex()
	nd, _ := s.Doc(d.ID)
	if nd == nil {
		nd = d
	}
	s.Hub.Publish(Event{Type: "doc:created", Data: nd})
	writeJSON(w, 201, nd)
}

func (s *Service) handleUpdateDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Content string `json:"content"`
		File    string `json:"file"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	d, err := s.Store.Update(id, body.Content, body.File)
	if err != nil {
		code := 400
		if errors.Is(err, store.ErrNotFound) {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	s.Reindex()
	nd, _ := s.Doc(d.ID)
	if nd == nil {
		nd = d
	}
	s.Hub.Publish(Event{Type: "doc:updated", Data: nd})
	writeJSON(w, 200, nd)
}

// handlePatchDoc currently handles retitling, which also rewrites inbound
// links so a rename cannot silently orphan references.
func (s *Service) handlePatchDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if body.Title == nil && body.Archived == nil {
		writeErr(w, 400, "nothing to change: supply title and/or archived")
		return
	}

	cur, ok := s.Doc(id)
	if !ok {
		writeErr(w, 404, "no such document")
		return
	}

	resp := map[string]any{}

	// Archiving is Quartzite state and works for either corpus — a read-only
	// vault note can be hidden without the vault being touched.
	if body.Archived != nil {
		if err := s.Archive.Set(id, *body.Archived); err != nil {
			writeErr(w, 500, "could not persist archive state: "+err.Error())
			return
		}
		resp["archived"] = *body.Archived
	}

	// Retitling writes to the file, so it remains store-only.
	if body.Title != nil {
		if strings.TrimSpace(*body.Title) == "" {
			writeErr(w, 400, "title cannot be empty")
			return
		}
		if !cur.Writable() {
			writeErr(w, 403, "vault documents are read-only")
			return
		}
		oldTitle := cur.Title
		if _, err := s.Store.Rename(id, *body.Title); err != nil {
			code := 400
			if errors.Is(err, store.ErrNotFound) {
				code = 404
			}
			writeErr(w, code, err.Error())
			return
		}
		resp["linksRewritten"] = s.Store.RewriteLinks(oldTitle, *body.Title)
	}

	s.Reindex()
	nd, _ := s.Doc(id)
	if nd == nil {
		nd = cur
	}
	resp["doc"] = nd

	evt := "doc:updated"
	if body.Archived != nil {
		evt = "doc:archived"
		if !*body.Archived {
			evt = "doc:unarchived"
		}
	}
	s.Hub.Publish(Event{Type: evt, Data: nd})
	writeJSON(w, 200, resp)
}

func (s *Service) handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.Doc(id)
	if !ok {
		writeErr(w, 404, "no such document")
		return
	}
	if !d.Writable() {
		writeErr(w, 403, "vault documents are read-only")
		return
	}
	if err := s.Store.Delete(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Reindex()
	s.Hub.Publish(Event{Type: "doc:deleted", Data: map[string]string{"id": id, "title": d.Title}})
	writeJSON(w, 200, map[string]string{"deleted": id})
}

func (s *Service) handleServeBundleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/d/"+r.PathValue("id")+"/", http.StatusMovedPermanently)
}

// handleServeBundle serves a document's bytes at a URL prefix of its own.
// Folders and zips take the same path here because both are fs.FS.
func (s *Service) handleServeBundle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.Doc(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	prefix := "/d/" + id + "/"
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")

	if !d.Bundled() {
		if rel != "" && rel != path.Base(d.Path) {
			http.NotFound(w, r)
			return
		}
		b, err := s.ReadDoc(d)
		if err != nil {
			http.Error(w, "cannot read document", 500)
			return
		}
		w.Header().Set("Content-Type", contentTypeFor(d.Format))
		w.Write(b)
		return
	}

	if d.Source != model.SourceStore {
		http.NotFound(w, r)
		return
	}
	fsys, closer, err := s.Store.BundleFS(d)
	if err != nil {
		http.Error(w, "cannot open bundle", 500)
		return
	}
	if closer != nil {
		defer closer.Close()
	}
	if rel == "" {
		rel = d.Entry
	}

	f, err := fsys.Open(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(model.FormatOf(rel)))
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, rel, st.ModTime(), rs)
		return
	}
	io.Copy(w, f)
}

// handleEvents streams index and document changes to the browser.
func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.Hub.Subscribe()
	defer cancel()

	fmt.Fprintf(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case b, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// contentTypeFor maps a format to a MIME type. Unknown formats are served as
// octet-stream so a stray file can never be interpreted as active content.
func contentTypeFor(format string) string {
	switch strings.ToLower(format) {
	case "html", "htm":
		return "text/html; charset=utf-8"
	case "md", "markdown":
		return "text/markdown; charset=utf-8"
	case "txt":
		return "text/plain; charset=utf-8"
	case "js", "mjs":
		return "text/javascript; charset=utf-8"
	case "css":
		return "text/css; charset=utf-8"
	case "json":
		return "application/json; charset=utf-8"
	case "svg":
		return "image/svg+xml"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "ico":
		return "image/x-icon"
	case "woff2":
		return "font/woff2"
	case "csv":
		return "text/csv; charset=utf-8"
	case "wasm":
		return "application/wasm"
	default:
		return "application/octet-stream"
	}
}
