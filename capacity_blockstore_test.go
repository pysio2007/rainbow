package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/stretchr/testify/require"
)

var errInjectedCapacityWrite = errors.New("injected capacity write failure")

type scanErrorBlockstore struct {
	blockstore.Blockstore
	scanErr error
}

func (s *scanErrorBlockstore) AllKeysChanWithError(context.Context) (<-chan cid.Cid, <-chan error, error) {
	errs := make(chan error, 1)
	errs <- s.scanErr
	close(errs)
	return make(chan cid.Cid), errs, nil
}

type blockingScanBlockstore struct {
	blockstore.Blockstore
	started chan struct{}
	release chan struct{}
}

func (s *blockingScanBlockstore) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return s.Blockstore.AllKeysChan(ctx)
}

type failingCapacityBlockstore struct {
	blockstore.Blockstore
	partial bool
}

func (s *failingCapacityBlockstore) Put(ctx context.Context, b blocks.Block) error {
	if s.partial {
		if err := s.Blockstore.Put(ctx, b); err != nil {
			return err
		}
		return errInjectedCapacityWrite
	}
	return s.Blockstore.Put(ctx, b)
}

func (s *failingCapacityBlockstore) PutMany(ctx context.Context, input []blocks.Block) error {
	if s.partial && len(input) > 0 {
		if err := s.Blockstore.Put(ctx, input[0]); err != nil {
			return err
		}
		return errInjectedCapacityWrite
	}
	return s.Blockstore.PutMany(ctx, input)
}

type failingCapacityDatastore struct {
	datastore.Batching
	failPuts bool
}

func (d *failingCapacityDatastore) Put(ctx context.Context, key datastore.Key, value []byte) error {
	if d.failPuts {
		return errInjectedCapacityWrite
	}
	return d.Batching.Put(ctx, key, value)
}

func newCapacityTestStore(t *testing.T, max int64) (blockstore.Blockstore, datastore.Batching, *CapacityBlockstore) {
	t.Helper()
	storage := dssync.MutexWrap(datastore.NewMapDatastore())
	metadata := dssync.MutexWrap(datastore.NewMapDatastore())
	inner := blockstore.NewBlockstore(storage, blockstore.NoPrefix())
	wrapped, err := NewCapacityBlockstore(t.Context(), inner, metadata, max)
	require.NoError(t, err)
	return inner, metadata, wrapped
}

func capacityBlock(t *testing.T, data string) blocks.Block {
	t.Helper()
	b := blocks.NewBlock([]byte(data))
	return b
}

func capacityCodecBlock(t *testing.T, data string, codec uint64) blocks.Block {
	t.Helper()
	b := capacityBlock(t, data)
	c := cid.NewCidV1(codec, b.Cid().Hash())
	result, err := blocks.NewBlockWithCid([]byte(data), c)
	require.NoError(t, err)
	return result
}

func TestCapacityBlockstoreDeduplicatesMultihash(t *testing.T) {
	_, _, bs := newCapacityTestStore(t, 10)
	a := capacityCodecBlock(t, "same", cid.Raw)
	b := capacityCodecBlock(t, "same", cid.DagProtobuf)
	require.NoError(t, bs.Put(t.Context(), a))
	require.NoError(t, bs.Put(t.Context(), b))
	require.Equal(t, int64(4), bs.PayloadBytes())
}

func TestCapacityBlockstoreZeroIsUnlimited(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 0)
	large := capacityBlock(t, "this is larger than zero")
	require.NoError(t, bs.Put(t.Context(), large))
	require.Equal(t, int64(len(large.RawData())), bs.PayloadBytes())
	has, err := inner.Has(t.Context(), large.Cid())
	require.NoError(t, err)
	require.True(t, has)
}

func TestCapacityBlockstoreEvictsFIFOAndDeletesAccounting(t *testing.T) {
	_, _, bs := newCapacityTestStore(t, 4)
	a, b, c := capacityBlock(t, "aa"), capacityBlock(t, "bb"), capacityBlock(t, "cc")
	require.NoError(t, bs.Put(t.Context(), a))
	require.NoError(t, bs.Put(t.Context(), b))
	require.NoError(t, bs.Put(t.Context(), c))
	has, err := bs.Has(t.Context(), a.Cid())
	require.NoError(t, err)
	require.False(t, has)
	has, err = bs.Has(t.Context(), b.Cid())
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, int64(4), bs.PayloadBytes())
	require.NoError(t, bs.DeleteBlock(t.Context(), b.Cid()))
	require.Equal(t, int64(2), bs.PayloadBytes())
}

func TestCapacityBlockstoreRejectsOversizedWithoutChangingState(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 4)
	a := capacityBlock(t, "ok")
	require.NoError(t, bs.Put(t.Context(), a))
	err := bs.Put(t.Context(), capacityBlock(t, "large"))
	var capacityErr *CapacityExceededError
	require.ErrorAs(t, err, &capacityErr)
	require.ErrorIs(t, err, ErrCapacityExceeded)
	require.Equal(t, int64(4), capacityErr.Maximum)
	require.Equal(t, int64(2), bs.PayloadBytes())
	has, err := inner.Has(t.Context(), capacityBlock(t, "large").Cid())
	require.NoError(t, err)
	require.False(t, has)
}

func TestCapacityBlockstorePutManyRejectsAtomically(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 4)
	a, b := capacityBlock(t, "aa"), capacityBlock(t, "bbb")
	err := bs.PutMany(t.Context(), []blocks.Block{a, b})
	require.ErrorIs(t, err, ErrCapacityExceeded)
	for _, b := range []blocks.Block{a, b} {
		has, hasErr := inner.Has(t.Context(), b.Cid())
		require.NoError(t, hasErr)
		require.False(t, has)
	}
	require.Zero(t, bs.PayloadBytes())
}

func TestCapacityBlockstoreReconcilesMissingAndDirtyMetadata(t *testing.T) {
	inner, metadata, _ := newCapacityTestStore(t, 4)
	a, b := capacityBlock(t, "aa"), capacityBlock(t, "bb")
	require.NoError(t, inner.Put(t.Context(), a))
	require.NoError(t, inner.Put(t.Context(), b))
	require.NoError(t, metadata.Delete(t.Context(), datastore.NewKey(capacityMetadataKey)))
	restarted, err := NewCapacityBlockstore(t.Context(), inner, metadata, 4)
	require.NoError(t, err)
	require.Equal(t, int64(4), restarted.PayloadBytes())
	require.NoError(t, metadata.Put(t.Context(), datastore.NewKey(capacityMetadataKey), []byte(`{"dirty":true}`)))
	restarted, err = NewCapacityBlockstore(t.Context(), inner, metadata, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), restarted.PayloadBytes())
}

func TestCapacityBlockstorePreservesFIFOAcrossRestart(t *testing.T) {
	inner, metadata, bs := newCapacityTestStore(t, 4)
	a, b := capacityBlock(t, "aa"), capacityBlock(t, "bb")
	require.NoError(t, bs.Put(t.Context(), a))
	require.NoError(t, bs.Put(t.Context(), b))
	restarted, err := NewCapacityBlockstore(t.Context(), inner, metadata, 4)
	require.NoError(t, err)
	require.NoError(t, restarted.Put(t.Context(), capacityBlock(t, "cc")))
	has, err := inner.Has(t.Context(), a.Cid())
	require.NoError(t, err)
	require.False(t, has)
}

func TestCapacityBlockstoreRepairsCorruptAndDuplicateMetadata(t *testing.T) {
	inner, metadata, _ := newCapacityTestStore(t, 2)
	a, b := capacityBlock(t, "aa"), capacityBlock(t, "bb")
	require.NoError(t, inner.Put(t.Context(), a))
	require.NoError(t, inner.Put(t.Context(), b))
	corrupt := capacityMetadata{
		Schema: 1,
		Order:  []string{capacityKey(a.Cid()), capacityKey(a.Cid()), capacityKey(b.Cid())},
		Sizes:  map[string]int64{capacityKey(a.Cid()): -1, capacityKey(b.Cid()): 2},
	}
	data, err := json.Marshal(corrupt)
	require.NoError(t, err)
	require.NoError(t, metadata.Put(t.Context(), datastore.NewKey(capacityMetadataKey), data))
	restarted, err := NewCapacityBlockstore(t.Context(), inner, metadata, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), restarted.PayloadBytes())
	has, err := inner.Has(t.Context(), a.Cid())
	require.NoError(t, err)
	hasB, err := inner.Has(t.Context(), b.Cid())
	require.NoError(t, err)
	require.NotEqual(t, has, hasB)
}

func TestCapacityBlockstoreStableDeduplicatesStoredFIFOEntries(t *testing.T) {
	inner, metadata, _ := newCapacityTestStore(t, 2)
	a, b := capacityBlock(t, "aa"), capacityBlock(t, "bb")
	require.NoError(t, inner.Put(t.Context(), a))
	require.NoError(t, inner.Put(t.Context(), b))
	state, err := json.Marshal(capacityMetadataState{Schema: capacityMetadataSchema, NextSeq: 4})
	require.NoError(t, err)
	require.NoError(t, metadata.Put(t.Context(), datastore.NewKey(capacityMetadataKey), state))
	for seq, key := range map[uint64]string{1: capacityKey(a.Cid()), 2: capacityKey(a.Cid()), 3: capacityKey(b.Cid())} {
		entry, marshalErr := json.Marshal(persistedCapacityEntry{
			Seq: seq, Key: base64.RawStdEncoding.EncodeToString([]byte(key)), Size: 2,
		})
		require.NoError(t, marshalErr)
		require.NoError(t, metadata.Put(t.Context(), capacityEntryKey(seq), entry))
	}
	_, err = NewCapacityBlockstore(t.Context(), inner, metadata, 2)
	require.NoError(t, err)
	has, err := inner.Has(t.Context(), a.Cid())
	require.NoError(t, err)
	require.False(t, has)
}

func TestCapacityBlockstoreRefusesWritesAfterPartialInnerFailure(t *testing.T) {
	storage := dssync.MutexWrap(datastore.NewMapDatastore())
	metadata := dssync.MutexWrap(datastore.NewMapDatastore())
	inner := blockstore.NewBlockstore(storage, blockstore.NoPrefix())
	bs, err := NewCapacityBlockstore(t.Context(), &failingCapacityBlockstore{Blockstore: inner, partial: true}, metadata, 20)
	require.NoError(t, err)
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "partial")), errInjectedCapacityWrite)
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "later")), ErrCapacityBlockstoreNeedsReconcile)
	bs.Blockstore.(*failingCapacityBlockstore).partial = false
	require.NoError(t, bs.Reconcile(t.Context()))
	require.NoError(t, bs.Put(t.Context(), capacityBlock(t, "after-reconcile")))
}

func TestCapacityBlockstoreRefusesWritesAfterMetadataFailure(t *testing.T) {
	storage := dssync.MutexWrap(datastore.NewMapDatastore())
	metadata := &failingCapacityDatastore{Batching: dssync.MutexWrap(datastore.NewMapDatastore())}
	inner := blockstore.NewBlockstore(storage, blockstore.NoPrefix())
	bs, err := NewCapacityBlockstore(t.Context(), inner, metadata, 20)
	require.NoError(t, err)
	metadata.failPuts = true
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "metadata-failure")), errInjectedCapacityWrite)
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "later")), ErrCapacityBlockstoreNeedsReconcile)
}

func TestCapacityBlockstoreReconcileCancellationLeavesItUnhealthy(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 20)
	blocking := &blockingScanBlockstore{Blockstore: inner, started: make(chan struct{}), release: make(chan struct{})}
	bs.Blockstore = blocking
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- bs.Reconcile(ctx) }()
	<-blocking.started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "after-cancel")), ErrCapacityBlockstoreNeedsReconcile)
}

func TestCapacityBlockstoreReconcilePropagatesScanError(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 20)
	bs.Blockstore = &scanErrorBlockstore{Blockstore: inner, scanErr: errInjectedCapacityWrite}
	require.ErrorIs(t, bs.Reconcile(t.Context()), errInjectedCapacityWrite)
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "after-scan-error")), ErrCapacityBlockstoreNeedsReconcile)
}

func TestCapacityBlockstoreReconcileGatesConcurrentPut(t *testing.T) {
	inner, _, bs := newCapacityTestStore(t, 4)
	first := capacityBlock(t, "aa")
	require.NoError(t, bs.Put(t.Context(), first))
	blocking := &blockingScanBlockstore{Blockstore: inner, started: make(chan struct{}), release: make(chan struct{})}
	bs.Blockstore = blocking
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- bs.Reconcile(t.Context()) }()
	<-blocking.started
	putDone := make(chan error, 1)
	go func() { putDone <- bs.Put(t.Context(), capacityBlock(t, "bb")) }()
	select {
	case err := <-putDone:
		t.Fatalf("put completed while reconciliation was scanning: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(blocking.release)
	require.NoError(t, <-reconcileDone)
	require.NoError(t, <-putDone)
	require.LessOrEqual(t, bs.PayloadBytes(), int64(4))
}

func TestCapacityBlockstoreRejectsSequenceOverflow(t *testing.T) {
	_, _, bs := newCapacityTestStore(t, 20)
	bs.mu.Lock()
	bs.state.NextSeq = ^uint64(0)
	bs.mu.Unlock()
	require.ErrorIs(t, bs.Put(t.Context(), capacityBlock(t, "overflow")), ErrCapacityBlockstoreNeedsReconcile)
}

func TestCapacityBlockstoreReindexesEmptySequenceMetadata(t *testing.T) {
	for _, nextSeq := range []uint64{0, ^uint64(0)} {
		t.Run(fmt.Sprintf("next-seq-%d", nextSeq), func(t *testing.T) {
			_, _, bs := newCapacityTestStore(t, 20)
			bs.mu.Lock()
			bs.state.NextSeq = nextSeq
			bs.mu.Unlock()
			require.NoError(t, bs.Reconcile(t.Context()))
			require.NoError(t, bs.Put(t.Context(), capacityBlock(t, "reindexed")))
		})
	}
}

func TestCapacityLocalScannerSkipsNonBlockDatastoreKeys(t *testing.T) {
	storage := dssync.MutexWrap(datastore.NewMapDatastore())
	metadata := dssync.MutexWrap(datastore.NewMapDatastore())
	require.NoError(t, storage.Put(t.Context(), datastore.NewKey("routing-record"), []byte("not a block")))
	inner := blockstore.NewBlockstore(storage, blockstore.NoPrefix())
	scanner := &capacityLocalScanner{Blockstore: inner, datastore: storage}
	bs, err := NewCapacityBlockstore(t.Context(), scanner, metadata, 2)
	require.NoError(t, err)
	a := capacityBlock(t, "aa")
	require.NoError(t, bs.Put(t.Context(), a))
	require.Equal(t, int64(2), bs.PayloadBytes())
}

func TestCapacityBlockstoreConcurrentPutsStayBounded(t *testing.T) {
	_, _, bs := newCapacityTestStore(t, 20)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = bs.Put(t.Context(), capacityBlock(t, string(rune('a'+i))))
		}(i)
	}
	wg.Wait()
	require.LessOrEqual(t, bs.PayloadBytes(), int64(20))
}

func TestCapacityExceededErrorIsSentinelWrapped(t *testing.T) {
	err := &CapacityExceededError{Maximum: 3, Requested: 4}
	require.True(t, errors.Is(err, ErrCapacityExceeded))
}
