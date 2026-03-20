// go-bt is a DHT-based magnet link search website.
//
// It crawls the BitTorrent DHT network to discover infohashes, fetches torrent
// metadata (name, size, file list) using the BEP9/BEP10 extension protocol,
// stores everything in a local SQLite database, and exposes a web interface
// for searching through the collected data.
//
// Usage:
//
//	go-bt [-addr :8080] [-db torrents.db] [-workers 10]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spectertian/go-bt/internal/dht"
	"github.com/spectertian/go-bt/internal/metadata"
	"github.com/spectertian/go-bt/internal/store"
	"github.com/spectertian/go-bt/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "torrents.db", "SQLite database file path")
	workers := flag.Int("workers", 10, "number of metadata-fetch workers")
	flag.Parse()

	// Initialise storage.
	db, err := store.New(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// hits carries (infohash, peer_addr) pairs discovered by the DHT crawler.
	hits := make(chan dht.Hit, 2000)

	var wg sync.WaitGroup

	// Start the DHT crawler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dht.NewCrawler(hits).Run(ctx)
	}()

	// Start metadata-fetch workers.
	fetcher := metadata.NewFetcher()
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metadataWorker(ctx, fetcher, db, hits)
		}()
	}

	// Start the web server.
	srv := web.NewServer(db, *addr)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("web: %v", err)
		}
	}()
	log.Printf("go-bt: server ready – open http://localhost%s", *addr)

	// Wait for SIGINT or SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("go-bt: shutting down…")
	cancel()
	wg.Wait()
	log.Println("go-bt: stopped")
}

// metadataWorker reads hits from the channel, fetches their metadata from the
// announcing peer, and saves the result to the database.
func metadataWorker(ctx context.Context, f *metadata.Fetcher, db *store.DB, ch <-chan dht.Hit) {
	// Track recently processed infohashes to avoid redundant fetches.
	seen := make(map[[20]byte]struct{}, 1024)

	for {
		select {
		case <-ctx.Done():
			return
		case hit := <-ch:
			if _, ok := seen[hit.InfoHash]; ok {
				continue
			}
			seen[hit.InfoHash] = struct{}{}
			// Trim the cache so it does not grow unboundedly.
			if len(seen) > 50000 {
				seen = make(map[[20]byte]struct{}, 1024)
			}

			info, err := f.Fetch(hit.InfoHash, hit.PeerAddr)
			if err != nil || info == nil || info.Name == "" {
				continue
			}
			if err := db.Save(hit.InfoHash, info.Name, info.TotalSize(), len(info.Files)); err != nil {
				log.Printf("store: save %x: %v", hit.InfoHash, err)
			} else {
				log.Printf("indexed: %s (%s)", info.Name, formatSize(info.TotalSize()))
			}
		}
	}
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
