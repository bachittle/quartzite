package api

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bachittle/quartzite/internal/graph"
	"github.com/bachittle/quartzite/internal/metrics"
	"github.com/bachittle/quartzite/internal/model"
	"github.com/bachittle/quartzite/internal/store"
	"github.com/bachittle/quartzite/internal/vault"
)

// Service joins the two corpora behind one index and one link graph.
type Service struct {
	Store   *store.Store
	Archive *store.Archive
	Metrics *metrics.Collector
	Hub     *Hub

	vaultRoot string

	mu    sync.RWMutex
	vidx  *vault.Index
	all   []*model.Doc
	byID  map[string]*model.Doc
	g     *graph.Graph
	stats model.Stats
}

// NewService wires a service and performs the first index.
func NewService(st *store.Store, vaultRoot string, mc *metrics.Collector) (*Service, error) {
	arch, err := store.OpenArchive(st.Root)
	if err != nil {
		return nil, err
	}
	s := &Service{
		Store: st, Archive: arch, Metrics: mc, Hub: NewHub(),
		vaultRoot: vaultRoot,
		byID:      map[string]*model.Doc{},
	}
	if err := s.Reindex(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reindex rebuilds both corpora and the link graph.
func (s *Service) Reindex() error {
	start := time.Now()

	if err := s.Store.Scan(); err != nil {
		return err
	}
	vidx, err := vault.Scan(s.vaultRoot)
	if err != nil {
		return err
	}

	storeDocs := s.Store.Docs()
	all := make([]*model.Doc, 0, len(storeDocs)+len(vidx.Docs))
	all = append(all, storeDocs...)
	all = append(all, vidx.Docs...)

	g := graph.Build(all)

	byID := make(map[string]*model.Doc, len(all))
	st := model.Stats{ByKind: map[string]int{}, ByFormat: map[string]int{}}
	for _, d := range all {
		d.Archived = s.Archive.Is(d.ID)
		byID[d.ID] = d
		st.Docs++
		if d.Archived {
			st.Archived++
		}
		st.ByKind[string(d.Kind)]++
		st.ByFormat[d.Format]++
		st.Bytes += d.Size
		if d.Source == model.SourceStore {
			st.StoreDocs++
		} else {
			st.VaultDocs++
		}
	}
	st.Links, st.Broken = g.Totals()
	st.IndexedAt = time.Now().UTC()
	st.IndexTimeMS = float64(time.Since(start).Microseconds()) / 1000.0

	s.mu.Lock()
	s.vidx, s.all, s.byID, s.g, s.stats = vidx, all, byID, g, st
	s.mu.Unlock()
	return nil
}

// Docs returns every indexed document.
func (s *Service) Docs() []*model.Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*model.Doc(nil), s.all...)
}

// Doc looks up one document by ID.
func (s *Service) Doc(id string) (*model.Doc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.byID[id]
	return d, ok
}

// Stats returns the current index summary.
func (s *Service) Stats() model.Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Graph returns the current link graph snapshot.
func (s *Service) Graph() *graph.Graph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.g
}

// ReadDoc returns a document's raw bytes from whichever corpus holds it.
func (s *Service) ReadDoc(d *model.Doc) ([]byte, error) {
	if d.Source == model.SourceVault {
		s.mu.RLock()
		vidx := s.vidx
		s.mu.RUnlock()
		return vidx.Read(d.Path)
	}
	return s.Store.ReadFile(d)
}

// ViewerFor resolves the viewer document bound to a format.
//
// The binding is a convention rather than a registry file: a document at
// viewers/<format>.html claims that format. Adding a format means writing a
// document, which is the whole point.
// ViewerInfo describes one registered viewer.
//
// A format may have several: viewers/md.html is the base, and
// viewers/md.dark-academia.html is the variant named "dark-academia". This is
// how a design language becomes content rather than chrome.
type ViewerInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Path    string `json:"path"`
	Format  string `json:"format"`
	Variant string `json:"variant,omitempty"` // empty means the base viewer
	URL     string `json:"url"`
}

// parseViewerPath splits viewers/<format>[.<variant>].html into its parts.
func parseViewerPath(p string) (format, variant string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(p), "viewers/") {
		return "", "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(p, "viewers/"), ".html")
	if name == "" {
		return "", "", false
	}
	if i := strings.Index(name, "."); i > 0 {
		return strings.ToLower(name[:i]), name[i+1:], true
	}
	return strings.ToLower(name), "", true
}

// ViewersFor returns every viewer claiming a format, base first.
func (s *Service) ViewersFor(format string) []ViewerInfo {
	format = strings.ToLower(format)
	if format == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []ViewerInfo
	for _, d := range s.all {
		if d.Source != model.SourceStore {
			continue
		}
		f, variant, ok := parseViewerPath(d.Path)
		if !ok || f != format {
			continue
		}
		out = append(out, ViewerInfo{
			ID: d.ID, Title: d.Title, Path: d.Path,
			Format: f, Variant: variant, URL: "/d/" + d.ID + "/",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Variant == "") != (out[j].Variant == "") {
			return out[i].Variant == "" // base first
		}
		return out[i].Variant < out[j].Variant
	})
	return out
}

// ViewerFor resolves the viewer for a document.
//
// Precedence: the document's own `viewer:` declaration, then the caller's
// preferred variant, then the base viewer for the format. A document that names
// a viewer which does not exist falls back rather than failing to render.
func (s *Service) ViewerFor(d *model.Doc, prefer string) (ViewerInfo, bool) {
	candidates := s.ViewersFor(d.Format)
	if len(candidates) == 0 {
		return ViewerInfo{}, false
	}
	for _, want := range []string{d.Viewer, prefer} {
		if want == "" {
			continue
		}
		for _, c := range candidates {
			if strings.EqualFold(c.Variant, want) {
				return c, true
			}
		}
	}
	return candidates[0], true
}

// Viewers lists every registered viewer across all formats.
func (s *Service) Viewers() []ViewerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ViewerInfo
	for _, d := range s.all {
		if d.Source != model.SourceStore {
			continue
		}
		f, variant, ok := parseViewerPath(d.Path)
		if !ok {
			continue
		}
		out = append(out, ViewerInfo{
			ID: d.ID, Title: d.Title, Path: d.Path,
			Format: f, Variant: variant, URL: "/d/" + d.ID + "/",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Format != out[j].Format {
			return out[i].Format < out[j].Format
		}
		return out[i].Variant < out[j].Variant
	})
	return out
}

// Hit is one search result.
type Hit struct {
	*model.Doc
	Score int    `json:"score"`
	Why   string `json:"why"`
}

// Search ranks documents against a query. Title matches outrank body matches,
// and exact title matches outrank prefix matches, which is enough structure to
// make the result list feel deliberate without an inverted index.
func (s *Service) Search(q string, limit int) []Hit {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return nil
	}
	terms := strings.Fields(q)
	docs := s.Docs()
	var hits []Hit

	for _, d := range docs {
		if d.Archived {
			continue // hidden means hidden, including from search
		}
		title := strings.ToLower(d.Title)
		excerpt := strings.ToLower(d.Excerpt)
		pathLower := strings.ToLower(d.Path)
		tags := strings.ToLower(strings.Join(d.Tags, " "))

		score, why := 0, ""
		for _, t := range terms {
			switch {
			case title == t:
				score += 100
				why = "exact title"
			case strings.HasPrefix(title, t):
				score += 60
				if why == "" {
					why = "title"
				}
			case strings.Contains(title, t):
				score += 40
				if why == "" {
					why = "title"
				}
			case strings.Contains(tags, t):
				score += 25
				if why == "" {
					why = "tag"
				}
			case strings.Contains(pathLower, t):
				score += 15
				if why == "" {
					why = "path"
				}
			case strings.Contains(excerpt, t):
				score += 10
				if why == "" {
					why = "body"
				}
			default:
				score -= 5 // a missing term should cost, so AND-ish wins
			}
		}
		if score <= 0 {
			continue
		}
		if d.Source == model.SourceStore {
			score += 3 // mild preference for the live corpus
		}
		hits = append(hits, Hit{Doc: d, Score: score, Why: why})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Title < hits[j].Title
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// fingerprint summarises index state cheaply enough to poll.
func (s *Service) fingerprint() (int, int64, time.Time) {
	docs := s.Docs()
	var bytes int64
	var newest time.Time
	for _, d := range docs {
		bytes += d.Size
		if d.Modified.After(newest) {
			newest = d.Modified
		}
	}
	return len(docs), bytes, newest
}

// Watch rescans on an interval and publishes an event when anything changed.
// Polling rather than fsnotify keeps the dependency list empty, and at this
// corpus size a rescan costs single-digit milliseconds.
func (s *Service) Watch(interval time.Duration, stop <-chan struct{}) {
	n, b, t := s.fingerprint()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if err := s.Reindex(); err != nil {
				continue
			}
			n2, b2, t2 := s.fingerprint()
			if n2 != n || b2 != b || !t2.Equal(t) {
				n, b, t = n2, b2, t2
				s.Hub.Publish(Event{Type: "index", Data: s.Stats()})
			}
		}
	}
}
