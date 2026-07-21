package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	datastore "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

func TestStatsPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	s, err := NewStats(dir)
	if err != nil {
		t.Fatalf("NewStats: %v", err)
	}
	s.AddFile()
	s.AddFile()
	s.AddOriginBytes(1500)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewStats(dir)
	if err != nil {
		t.Fatalf("reopen NewStats: %v", err)
	}
	defer reopened.Close()
	files, bytes := reopened.Snapshot()
	if files != 2 || bytes != 1500 {
		t.Fatalf("after reopen got files=%d bytes=%d, want 2 and 1500", files, bytes)
	}
}

func TestStatsConcurrentAdds(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStats(dir)
	if err != nil {
		t.Fatalf("NewStats: %v", err)
	}
	defer s.Close()

	var wg sync.WaitGroup
	const goroutines, perG = 8, 1000
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.AddFile()
				s.AddOriginBytes(2)
			}
		}()
	}
	wg.Wait()

	files, bytes := s.Snapshot()
	if want := int64(goroutines * perG); files != want {
		t.Fatalf("files=%d, want %d", files, want)
	}
	if want := int64(goroutines * perG * 2); bytes != want {
		t.Fatalf("bytes=%d, want %d", bytes, want)
	}
}

func TestStatsNilSafe(t *testing.T) {
	var s *Stats
	s.AddFile()
	s.AddOriginBytes(10)
	if files, bytes := s.Snapshot(); files != 0 || bytes != 0 {
		t.Fatalf("nil Snapshot got %d/%d, want 0/0", files, bytes)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestStatsBlockstoreCountsNewBytes(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStats(dir)
	if err != nil {
		t.Fatalf("NewStats: %v", err)
	}
	defer s.Close()

	inner := blockstore.NewBlockstore(dssync.MutexWrap(datastore.NewMapDatastore()), blockstore.NoPrefix())
	bs := &statsBlockstore{Blockstore: inner, stats: s}

	ctx := context.Background()
	b1 := blocks.NewBlock([]byte("hello"))   // 5 bytes
	b2 := blocks.NewBlock([]byte("world!!")) // 7 bytes
	if err := bs.Put(ctx, b1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := bs.PutMany(ctx, []blocks.Block{b2}); err != nil {
		t.Fatalf("PutMany: %v", err)
	}

	if _, bytes := s.Snapshot(); bytes != 12 {
		t.Fatalf("originBytes=%d, want 12", bytes)
	}
}

func TestWithStatsCounterOnlyCountsSuccess(t *testing.T) {
	s, err := NewStats(t.TempDir())
	if err != nil {
		t.Fatalf("NewStats: %v", err)
	}
	defer s.Close()

	makeHandler := func(code int) http.Handler {
		wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
		return withStatsCounter(wrapped, s)
	}

	rec := httptest.NewRecorder()
	makeHandler(http.StatusOK).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ipfs/foo", nil))
	rec = httptest.NewRecorder()
	makeHandler(http.StatusNotFound).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ipfs/bar", nil))

	if files, _ := s.Snapshot(); files != 1 {
		t.Fatalf("filesProcessed=%d, want 1 (only the 200 response)", files)
	}
}

func TestStatsHandlerReturnsJSON(t *testing.T) {
	s, err := NewStats(t.TempDir())
	if err != nil {
		t.Fatalf("NewStats: %v", err)
	}
	defer s.Close()
	s.AddFile()
	s.AddOriginBytes(2048)

	rec := httptest.NewRecorder()
	statsHandler(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, statsAPIPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	var body statsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != 1 || body.FilesProcessed != 1 || body.OriginBytes != 2048 {
		t.Fatalf("got %+v, want {1 1 2048}", body)
	}
}
