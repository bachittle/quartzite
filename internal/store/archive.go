package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Archive tracks which documents are hidden from the default views.
//
// State lives beside the store rather than inside the documents, for two
// reasons. It is Quartzite's opinion about a document, not part of the
// document — so archiving must not dirty a file an agent wrote. And because it
// is external, a read-only vault note can be archived without the vault ever
// being written to, which keeps the safety guarantee intact.
//
// Persisted server-side, so the state is the same in every browser.
type Archive struct {
	path string

	mu  sync.RWMutex
	ids map[string]bool
}

// OpenArchive loads archive state from a store's metadata directory.
func OpenArchive(storeRoot string) (*Archive, error) {
	dir := filepath.Join(storeRoot, metaDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	a := &Archive{path: filepath.Join(dir, "archived.json"), ids: map[string]bool{}}
	a.load()
	return a, nil
}

func (a *Archive) load() {
	b, err := os.ReadFile(a.path)
	if err != nil {
		return
	}
	var list []string
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, id := range list {
		a.ids[id] = true
	}
}

// save writes atomically. Caller holds the lock.
func (a *Archive) save() error {
	list := make([]string, 0, len(a.ids))
	for id := range a.ids {
		list = append(list, id)
	}
	sort.Strings(list) // stable on disk, so the file diffs cleanly

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// Is reports whether a document is archived.
func (a *Archive) Is(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ids[id]
}

// Set archives or restores a document and persists immediately.
func (a *Archive) Set(id string, archived bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if archived == a.ids[id] {
		return nil
	}
	if archived {
		a.ids[id] = true
	} else {
		delete(a.ids, id)
	}
	return a.save()
}

// Count returns how many documents are archived.
func (a *Archive) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.ids)
}

// IDs returns the archived set.
//
// Entries whose document no longer exists are deliberately left in place. A
// vault that is briefly unreadable would otherwise look like a mass deletion
// and silently discard the user's intent; a few stale identifiers cost nothing.
func (a *Archive) IDs() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.ids))
	for id := range a.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
