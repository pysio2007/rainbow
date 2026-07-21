package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	_ "modernc.org/sqlite"
)

// statsFlushInterval is how often in-memory counters are persisted to disk.
// Counters live in memory on the hot path; the SQLite file only holds the
// latest absolute totals, so it stays a few KB regardless of traffic.
const statsFlushInterval = 10 * time.Second

// Stats tracks minimal, process-cumulative gateway usage counters backed by a
// tiny single-row SQLite table. All hot-path updates are lock-free atomic adds;
// values are flushed to disk periodically and on Close.
type Stats struct {
	db    *sql.DB
	files atomic.Int64
	bytes atomic.Int64

	started  atomic.Bool
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewStats opens (or creates) <dataDir>/stats.db, ensures the schema exists, and
// loads existing totals so counters keep accumulating across restarts.
func NewStats(dataDir string) (*Stats, error) {
	path := filepath.Join(dataDir, "stats.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening stats db: %w", err)
	}
	// Single writer, so a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=TRUNCATE",
		"PRAGMA synchronous=NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("stats pragma %q: %w", p, err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS stats (
  id               INTEGER PRIMARY KEY CHECK (id = 1),
  files_processed  INTEGER NOT NULL DEFAULT 0,
  origin_bytes     INTEGER NOT NULL DEFAULT 0,
  updated_at       INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO stats (id) VALUES (1);`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating stats schema: %w", err)
	}

	s := &Stats{
		db:   db,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	var files, bytes int64
	row := db.QueryRow("SELECT files_processed, origin_bytes FROM stats WHERE id = 1")
	if err := row.Scan(&files, &bytes); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("loading stats: %w", err)
	}
	s.files.Store(files)
	s.bytes.Store(bytes)

	return s, nil
}

// AddFile records one successfully served gateway request. Nil-safe.
func (s *Stats) AddFile() {
	if s == nil {
		return
	}
	s.files.Add(1)
}

// AddOriginBytes records bytes fetched from origin (new blocks written to the
// local blockstore). Nil-safe.
func (s *Stats) AddOriginBytes(n int64) {
	if s == nil || n <= 0 {
		return
	}
	s.bytes.Add(n)
}

// Snapshot returns the current cumulative counters. Nil-safe.
func (s *Stats) Snapshot() (files, bytes int64) {
	if s == nil {
		return 0, 0
	}
	return s.files.Load(), s.bytes.Load()
}

// Run periodically flushes counters to disk until ctx is cancelled or Close is
// called. Intended to be started in its own goroutine.
func (s *Stats) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.started.Store(true)
	defer close(s.done)
	ticker := time.NewTicker(statsFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			if err := s.flush(); err != nil {
				goLog.Warnf("flushing stats: %s", err)
			}
		}
	}
}

// flush persists the current in-memory totals as a single-row update.
func (s *Stats) flush() error {
	files, bytes := s.Snapshot()
	_, err := s.db.Exec(
		"UPDATE stats SET files_processed = ?, origin_bytes = ?, updated_at = ? WHERE id = 1",
		files, bytes, time.Now().Unix(),
	)
	return err
}

// Close stops the background flusher, persists final counters, and closes the
// database. Nil-safe and idempotent.
func (s *Stats) Close() error {
	if s == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		close(s.stop)
		if s.started.Load() {
			<-s.done
		}
	})
	flushErr := s.flush()
	closeErr := s.db.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// statsBlockstore wraps a blockstore and counts the payload bytes of blocks
// newly written to disk. Because bitswap only writes blocks we didn't already
// hold, this approximates "origin" (back-to-network) traffic.
type statsBlockstore struct {
	blockstore.Blockstore
	stats *Stats
}

func (b *statsBlockstore) Put(ctx context.Context, block blocks.Block) error {
	if err := b.Blockstore.Put(ctx, block); err != nil {
		return err
	}
	b.stats.AddOriginBytes(int64(len(block.RawData())))
	return nil
}

func (b *statsBlockstore) PutMany(ctx context.Context, blks []blocks.Block) error {
	if err := b.Blockstore.PutMany(ctx, blks); err != nil {
		return err
	}
	var total int64
	for _, block := range blks {
		total += int64(len(block.RawData()))
	}
	b.stats.AddOriginBytes(total)
	return nil
}
