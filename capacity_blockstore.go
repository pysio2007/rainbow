package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	datastore "github.com/ipfs/go-datastore"
	"github.com/multiformats/go-multihash"
)

var ErrCapacityExceeded = errors.New("blockstore capacity exceeded")
var ErrCapacityBlockstoreNeedsReconcile = errors.New("capacity blockstore needs reconciliation")

const capacityMaxSequence = ^uint64(0)

type CapacityExceededError struct {
	Maximum   int64
	Requested int64
}

func (e *CapacityExceededError) Error() string {
	return fmt.Sprintf("%s: requested %d payload bytes, maximum is %d", ErrCapacityExceeded, e.Requested, e.Maximum)
}

func (e *CapacityExceededError) Unwrap() error { return ErrCapacityExceeded }

type CapacityBlockstore struct {
	blockstore.Blockstore
	metadata      *capacityMetadataStore
	maximum       int64
	mu            sync.Mutex
	reconcileGate sync.RWMutex
	state         capacityMetadataState
	order         []capacityEntry
	sizes         map[string]int64
	unhealthy     bool
}

type capacityErrorKeyScanner interface {
	AllKeysChanWithError(context.Context) (<-chan cid.Cid, <-chan error, error)
}

func NewCapacityBlockstore(ctx context.Context, inner blockstore.Blockstore, metadata datastore.Batching, maximum int64) (*CapacityBlockstore, error) {
	if maximum < 0 {
		return nil, fmt.Errorf("negative capacity: %d", maximum)
	}
	store := newCapacityMetadataStore(metadata)
	state, invalid, err := store.load(ctx)
	if err != nil {
		return nil, err
	}
	bs := &CapacityBlockstore{
		Blockstore: inner,
		metadata:   store,
		maximum:    maximum,
		state:      state,
		sizes:      make(map[string]int64),
	}
	bs.order = metadataOrder(store.entries)
	for _, entry := range bs.order {
		bs.sizes[entry.Key] = entry.Size
	}
	if invalid {
		bs.state.Dirty = true
	}
	return bs, bs.Reconcile(ctx)
}

func metadataOrder(entries map[uint64]capacityEntry) []capacityEntry {
	order := make([]capacityEntry, 0, len(entries))
	for _, entry := range entries {
		order = append(order, entry)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Seq < order[j].Seq })
	return order
}

func (bs *CapacityBlockstore) PayloadBytes() int64 {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.state.Total
}

func (bs *CapacityBlockstore) Put(ctx context.Context, b blocks.Block) error {
	return bs.putMany(ctx, []blocks.Block{b}, true)
}

func (bs *CapacityBlockstore) PutMany(ctx context.Context, input []blocks.Block) error {
	return bs.putMany(ctx, input, false)
}

func (bs *CapacityBlockstore) putMany(ctx context.Context, input []blocks.Block, single bool) error {
	bs.reconcileGate.RLock()
	defer bs.reconcileGate.RUnlock()
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.unhealthy {
		return ErrCapacityBlockstoreNeedsReconcile
	}
	if len(input) == 0 {
		return nil
	}
	unique := make([]blocks.Block, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	var incremental int64
	for _, b := range input {
		key := capacityKey(b.Cid())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		exists, err := bs.Blockstore.Has(ctx, b.Cid())
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		incremental += int64(len(b.RawData()))
		unique = append(unique, b)
	}
	if bs.maximum > 0 && incremental > bs.maximum {
		return &CapacityExceededError{Maximum: bs.maximum, Requested: incremental}
	}
	if len(unique) == 0 {
		return nil
	}
	if bs.state.NextSeq == 0 || bs.state.NextSeq == capacityMaxSequence {
		return bs.unhealthyErrorLocked(fmt.Errorf("%w: capacity metadata sequence exhausted", ErrCapacityBlockstoreNeedsReconcile))
	}
	need := int64(0)
	if bs.maximum > 0 && bs.state.Total+incremental > bs.maximum {
		need = bs.state.Total + incremental - bs.maximum
	}
	if need > 0 && len(bs.order) == 0 {
		return bs.unhealthyErrorLocked(errors.New("capacity accounting cannot make room"))
	}
	if err := bs.metadata.markDirty(ctx, &bs.state); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	removed, err := bs.evictLocked(ctx, need)
	if err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	var putErr error
	if single {
		putErr = bs.Blockstore.Put(ctx, unique[0])
	} else {
		putErr = bs.Blockstore.PutMany(ctx, unique)
	}
	if putErr != nil {
		return bs.unhealthyErrorLocked(putErr)
	}
	added := make([]capacityEntry, 0, len(unique))
	for _, b := range unique {
		key := capacityKey(b.Cid())
		size := int64(len(b.RawData()))
		entry := capacityEntry{Seq: bs.state.NextSeq, Key: key, Size: size}
		if entry.Seq == 0 {
			entry.Seq = 1
		}
		bs.state.NextSeq = entry.Seq + 1
		bs.order = append(bs.order, entry)
		bs.sizes[key] = size
		bs.state.Total += size
		added = append(added, entry)
	}
	if err := bs.persistDeltaLocked(ctx, added, removed); err != nil {
		return err
	}
	return nil
}

func (bs *CapacityBlockstore) DeleteBlock(ctx context.Context, c cid.Cid) error {
	bs.reconcileGate.RLock()
	defer bs.reconcileGate.RUnlock()
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.unhealthy {
		return ErrCapacityBlockstoreNeedsReconcile
	}
	has, err := bs.Blockstore.Has(ctx, c)
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	if err := bs.metadata.markDirty(ctx, &bs.state); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	if err := bs.Blockstore.DeleteBlock(ctx, c); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	key := capacityKey(c)
	removed := bs.entriesForKey(key)
	bs.removeLocked(key)
	return bs.persistDeltaLocked(ctx, nil, removed)
}

func (bs *CapacityBlockstore) Reconcile(ctx context.Context) error {
	bs.reconcileGate.Lock()
	defer bs.reconcileGate.Unlock()
	found, err := bs.scan(ctx)
	if err != nil {
		bs.setUnhealthy()
		return err
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()
	wasDirty := bs.state.Dirty
	order := bs.reconciledOrder(found, wasDirty)
	if err := bs.metadata.markDirty(ctx, &bs.state); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	if bs.maximum > 0 {
		var total int64
		for _, entry := range order {
			total += found[entry.Key]
		}
		for total > bs.maximum && len(order) > 0 {
			entry := order[0]
			c := cid.NewCidV1(cid.Raw, multihash.Multihash([]byte(entry.Key)))
			if err := bs.Blockstore.DeleteBlock(ctx, c); err != nil {
				return bs.unhealthyErrorLocked(err)
			}
			total -= found[entry.Key]
			delete(found, entry.Key)
			order = order[1:]
		}
	}
	bs.order = order
	bs.sizes = found
	bs.state.Total = 0
	for _, entry := range order {
		bs.state.Total += entry.Size
	}
	if err := bs.persistLocked(ctx); err != nil {
		return err
	}
	bs.unhealthy = false
	return nil
}

func (bs *CapacityBlockstore) scan(ctx context.Context) (map[string]int64, error) {
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	found := make(map[string]int64)
	var keys <-chan cid.Cid
	var scanErrors <-chan error
	var err error
	if scanner, ok := bs.Blockstore.(capacityErrorKeyScanner); ok {
		keys, scanErrors, err = scanner.AllKeysChanWithError(scanCtx)
	} else {
		keys, err = bs.Blockstore.AllKeysChan(scanCtx)
	}
	if err != nil {
		return nil, err
	}
	for keys != nil || scanErrors != nil {
		select {
		case c, ok := <-keys:
			if !ok {
				keys = nil
				if scanErrors == nil {
					if err := scanCtx.Err(); err != nil {
						return nil, err
					}
				}
				continue
			}
			sz, err := bs.Blockstore.GetSize(scanCtx, c)
			if err != nil {
				return nil, err
			}
			if sz < 0 {
				return nil, fmt.Errorf("negative block size for %s: %d", c, sz)
			}
			found[capacityKey(c)] = int64(sz)
		case scanErr, ok := <-scanErrors:
			if ok && scanErr != nil {
				return nil, scanErr
			}
			scanErrors = nil
			if keys == nil {
				if err := scanCtx.Err(); err != nil {
					return nil, err
				}
				return found, nil
			}
		case <-scanCtx.Done():
			return nil, scanCtx.Err()
		}
	}
	if err := scanCtx.Err(); err != nil {
		return nil, err
	}
	return found, nil
}

func (bs *CapacityBlockstore) reconciledOrder(found map[string]int64, dirty bool) []capacityEntry {
	used := make(map[string]struct{}, len(found))
	order := make([]capacityEntry, 0, len(found))
	if !dirty {
		for _, entry := range bs.order {
			if _, ok := found[entry.Key]; !ok {
				continue
			}
			if _, duplicate := used[entry.Key]; duplicate {
				continue
			}
			entry.Size = found[entry.Key]
			order = append(order, entry)
			used[entry.Key] = struct{}{}
		}
	}
	missing := make([]string, 0, len(found)-len(order))
	for key := range found {
		if _, ok := used[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if bs.state.NextSeq == 0 || bs.state.NextSeq == capacityMaxSequence {
		for i := range order {
			order[i].Seq = uint64(i + 1)
		}
		bs.state.NextSeq = uint64(len(order) + 1)
	}
	for _, key := range missing {
		entry := capacityEntry{Seq: bs.state.NextSeq, Key: key, Size: found[key]}
		if entry.Seq == 0 {
			entry.Seq = 1
		}
		bs.state.NextSeq = entry.Seq + 1
		order = append(order, entry)
	}
	return order
}

func (bs *CapacityBlockstore) evictLocked(ctx context.Context, bytes int64) ([]capacityEntry, error) {
	removed := make([]capacityEntry, 0)
	for bytes > 0 && len(bs.order) > 0 {
		entry := bs.order[0]
		c := cid.NewCidV1(cid.Raw, multihash.Multihash([]byte(entry.Key)))
		if err := bs.Blockstore.DeleteBlock(ctx, c); err != nil {
			return removed, err
		}
		bs.removeLocked(entry.Key)
		removed = append(removed, entry)
		bytes -= entry.Size
	}
	if bytes > 0 {
		return removed, errors.New("capacity accounting could not evict enough data")
	}
	return removed, nil
}

func (bs *CapacityBlockstore) entriesForKey(key string) []capacityEntry {
	entries := make([]capacityEntry, 0, 1)
	for _, entry := range bs.order {
		if entry.Key == key {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (bs *CapacityBlockstore) removeLocked(key string) {
	if size, ok := bs.sizes[key]; ok {
		bs.state.Total -= size
		delete(bs.sizes, key)
	}
	filtered := bs.order[:0]
	for _, entry := range bs.order {
		if entry.Key != key {
			filtered = append(filtered, entry)
		}
	}
	bs.order = filtered
}

func (bs *CapacityBlockstore) persistLocked(ctx context.Context) error {
	if err := bs.metadata.replace(ctx, bs.order, &bs.state); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	return nil
}

func (bs *CapacityBlockstore) persistDeltaLocked(ctx context.Context, added, removed []capacityEntry) error {
	if err := bs.metadata.commitDelta(ctx, added, removed, &bs.state); err != nil {
		return bs.unhealthyErrorLocked(err)
	}
	return nil
}

func (bs *CapacityBlockstore) setUnhealthy() {
	bs.mu.Lock()
	wasHealthy := !bs.unhealthy
	bs.unhealthy = true
	bs.state.Dirty = true
	state := bs.state
	bs.mu.Unlock()
	_ = bs.metadata.writeState(context.Background(), state)
	if wasHealthy {
		goLog.Errorf("capacity blockstore marked unhealthy; new writes will be rejected until the process restarts and reconciles: initial reconciliation scan failed")
	}
}

func (bs *CapacityBlockstore) unhealthyErrorLocked(err error) error {
	wasHealthy := !bs.unhealthy
	bs.unhealthy = true
	bs.state.Dirty = true
	_ = bs.metadata.writeState(context.Background(), bs.state)
	if wasHealthy {
		goLog.Errorf("capacity blockstore marked unhealthy; new writes will be rejected until the process restarts and reconciles: %s", err)
	}
	return err
}

func capacityKey(c cid.Cid) string { return string(c.Hash()) }
