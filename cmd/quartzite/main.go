// Command quartzite serves the Quartzite knowledge base.
//
// One binary, no dependencies. Paths are resolved relative to the executable's
// project root by default, so moving the folder does not break the install.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bachittle/quartzite/internal/api"
	"github.com/bachittle/quartzite/internal/metrics"
	"github.com/bachittle/quartzite/internal/store"
)

func main() {
	root := defaultRoot()

	addr := flag.String("addr", envOr("QUARTZITE_ADDR", ":5003"), "listen address")
	storeDir := flag.String("store", envOr("QUARTZITE_STORE", filepath.Join(root, "store")),
		"read-write document store; created and seeded if it does not exist yet")
	vaultDir := flag.String("vault", envOr("QUARTZITE_VAULT", ""),
		"read-only folder of existing notes (indexed, never written); empty runs store-only")
	webDir := flag.String("web", filepath.Join(root, "web"), "static frontend directory")
	watch := flag.Duration("watch", 3*time.Second, "rescan interval; 0 disables")
	flag.Parse()

	if err := seedIfNew(*storeDir, filepath.Join(root, "seed-store")); err != nil {
		log.Fatalf("quartzite: cannot seed store at %s: %v", *storeDir, err)
	}

	st, err := store.Open(*storeDir)
	if err != nil {
		log.Fatalf("quartzite: cannot open store at %s: %v", *storeDir, err)
	}

	mc := metrics.New()
	svc, err := api.NewService(st, *vaultDir, mc)
	if err != nil {
		log.Fatalf("quartzite: initial index failed: %v", err)
	}

	stats := svc.Stats()
	log.Printf("quartzite: indexed %d documents in %.1fms (%d store, %d vault, %d links, %d broken)",
		stats.Docs, stats.IndexTimeMS, stats.StoreDocs, stats.VaultDocs, stats.Links, stats.Broken)
	if *vaultDir == "" {
		log.Print("quartzite: no vault configured, running with store only")
	} else if _, err := os.Stat(*vaultDir); err != nil {
		log.Printf("quartzite: vault %s not readable, running with store only", *vaultDir)
	}

	stop := make(chan struct{})
	if *watch > 0 {
		go svc.Watch(*watch, stop)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           svc.Routes(*webDir),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: SSE connections are intentionally long-lived.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("quartzite: listening on http://localhost%s  (store=%s vault=%s)", *addr, *storeDir, *vaultDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("quartzite: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	close(stop)

	fmt.Println()
	log.Print("quartzite: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("quartzite: forced shutdown: %v", err)
	}
}

// envOr reads an environment variable, falling back to def when unset or
// empty. Flags still win: an explicit -flag overrides whatever env supplied.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// seedIfNew copies the repo's seed-store into a store that does not exist yet
// (or exists but holds no documents), so opening a brand-new store still gives
// you a working app. Viewers are documents, so an unseeded empty store would
// render nothing but markdown.
func seedIfNew(dst, seed string) error {
	if entries, err := os.ReadDir(dst); err == nil {
		for _, e := range entries {
			if e.Name() != ".quartzite" && e.Name() != ".DS_Store" {
				return nil // store already has content; never overwrite
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(seed); errors.Is(err, fs.ErrNotExist) {
		return nil // no seed shipped alongside the binary; open the store bare
	}
	if err := os.CopyFS(dst, os.DirFS(seed)); err != nil {
		return err
	}
	log.Printf("quartzite: new store at %s seeded from %s", dst, seed)
	return nil
}

// defaultRoot finds the project root so the binary works whether it is run
// from the project directory or invoked by absolute path from elsewhere.
func defaultRoot() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 4; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
