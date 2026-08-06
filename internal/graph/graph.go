// Package graph resolves [[wikilinks]] into a navigable link graph.
//
// Links are written by title, the way Obsidian does it, because the vault is
// read-only and already full of them. IDs exist for the API, where a stable
// reference matters more than a readable one.
package graph

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bachittle/quartzite/internal/model"
)

// wikilinkRe matches [[Target]], [[Target|alias]] and [[Target#heading]].
var wikilinkRe = regexp.MustCompile(`\[\[([^\]\|#]+)(?:[#\|][^\]]*)?\]\]`)

// embedRe matches ![[Target]], the transclusion form. An embed is also a link.
var embedRe = regexp.MustCompile(`!\[\[([^\]\|#]+)(?:[#\|][^\]]*)?\]\]`)

// tagRe matches #tag, avoiding markdown headings and URL fragments.
var tagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][\w/\-]*)`)

// hexRe matches CSS colour literals, which otherwise read as tags in any
// document that contains a stylesheet.
var hexRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ExtractLinks returns the distinct wikilink targets in a document body,
// preserving the order they appear.
func ExtractLinks(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
		t := strings.TrimSpace(m[1])
		if t == "" {
			continue
		}
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out
}

// ExtractEmbeds returns the targets that are transcluded rather than merely
// referenced. These are the ones a viewer renders inline.
func ExtractEmbeds(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range embedRe.FindAllStringSubmatch(body, -1) {
		t := strings.TrimSpace(m[1])
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// ExtractTags returns inline #tags found in a body.
func ExtractTags(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		t := m[1]
		k := strings.ToLower(t)
		if seen[k] || hexRe.MatchString(t) {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Ref is a resolved inbound link.
type Ref struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Source model.Source `json:"source"`
}

// Graph is an immutable snapshot of link structure. It is rebuilt wholesale on
// reindex rather than mutated, so readers never need to lock.
type Graph struct {
	byTitle   map[string]*model.Doc // lowercased title -> doc
	byPath    map[string]*model.Doc // lowercased path and path-stem -> doc
	byBase    map[string]*model.Doc // lowercased base filename, no extension -> doc
	backlinks map[string][]Ref      // doc ID -> inbound refs
	broken    map[string][]string   // doc ID -> unresolved targets
	total     int
	brokenN   int
}

// Build indexes a set of documents into a link graph. When two documents share
// a title, the store wins: it holds the newer, agent-authored material, and the
// vault copy stays reachable by ID.
func Build(docs []*model.Doc) *Graph {
	g := &Graph{
		byTitle:   make(map[string]*model.Doc, len(docs)),
		byPath:    make(map[string]*model.Doc, len(docs)*2),
		byBase:    make(map[string]*model.Doc, len(docs)),
		backlinks: make(map[string][]Ref),
		broken:    make(map[string][]string),
	}

	for _, d := range docs {
		k := strings.ToLower(d.Title)
		if prev, ok := g.byTitle[k]; ok {
			if prev.Source == model.SourceStore && d.Source != model.SourceStore {
				continue // keep the store's copy
			}
		}
		g.byTitle[k] = d
	}

	// Obsidian also allows a link to name a path rather than a title, so index
	// both the full path and the path without its extension.
	for _, d := range docs {
		p := strings.ToLower(d.Path)
		if _, taken := g.byPath[p]; !taken {
			g.byPath[p] = d
		}
		if ext := strings.LastIndexByte(p, '.'); ext > 0 {
			stem := p[:ext]
			if _, taken := g.byPath[stem]; !taken {
				g.byPath[stem] = d
			}
		}
	}

	// Obsidian's shortest form links by bare filename — [[2026-07-28]] finds
	// Daily Notes/2026-07-28.md. Titles can't carry this (TitleOf rewrites
	// hyphens to spaces), so index the base name too, store winning collisions
	// under the same rule as titles.
	for _, d := range docs {
		base := strings.ToLower(path.Base(d.Path))
		if ext := strings.LastIndexByte(base, '.'); ext > 0 {
			base = base[:ext]
		}
		if base == "" || base == "." || base == "/" {
			continue
		}
		if prev, ok := g.byBase[base]; ok {
			if prev.Source == model.SourceStore && d.Source != model.SourceStore {
				continue // keep the store's copy
			}
			if prev.Source == d.Source {
				continue // first sighting wins within a corpus, like byPath
			}
		}
		g.byBase[base] = d
	}

	for _, d := range docs {
		for _, target := range d.Links {
			g.total++
			dst, ok := g.resolve(target)
			if !ok {
				g.brokenN++
				g.broken[d.ID] = append(g.broken[d.ID], target)
				continue
			}
			if dst.ID == d.ID {
				continue // don't count self-links as backlinks
			}
			g.backlinks[dst.ID] = append(g.backlinks[dst.ID], Ref{
				ID: d.ID, Title: d.Title, Source: d.Source,
			})
		}
	}

	for id := range g.backlinks {
		refs := g.backlinks[id]
		sort.Slice(refs, func(i, j int) bool { return refs[i].Title < refs[j].Title })
	}
	return g
}

// resolve looks a target up by title first, then by path, then by bare
// filename, matching Obsidian's precedence.
func (g *Graph) resolve(target string) (*model.Doc, bool) {
	k := strings.ToLower(strings.TrimSpace(target))
	if d, ok := g.byTitle[k]; ok {
		return d, true
	}
	if d, ok := g.byPath[k]; ok {
		return d, true
	}
	if d, ok := g.byBase[k]; ok {
		return d, true
	}
	return nil, false
}

// Resolve maps a wikilink target to a document, if one exists.
func (g *Graph) Resolve(target string) (*model.Doc, bool) { return g.resolve(target) }

// Backlinks returns documents linking to the given ID.
func (g *Graph) Backlinks(id string) []Ref { return g.backlinks[id] }

// Broken returns unresolved link targets for the given ID.
func (g *Graph) Broken(id string) []string { return g.broken[id] }

// Totals reports overall link counts for the dashboard.
func (g *Graph) Totals() (total, broken int) { return g.total, g.brokenN }
