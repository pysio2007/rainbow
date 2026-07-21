package main

import (
	"context"
	"testing"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingSizeBlockstore struct {
	blockstore.Blockstore
	failing cid.Cid
	deleted []cid.Cid
}

func (s *failingSizeBlockstore) AllKeysChan(context.Context) (<-chan cid.Cid, error) {
	keys := make(chan cid.Cid, 2)
	keys <- s.failing
	keys <- blocks.NewBlock([]byte("ok")).Cid()
	close(keys)
	return keys, nil
}

func (s *failingSizeBlockstore) GetSize(ctx context.Context, c cid.Cid) (int, error) {
	if c == s.failing {
		return 0, assert.AnError
	}
	return s.Blockstore.GetSize(ctx, c)
}

func (s *failingSizeBlockstore) DeleteBlock(ctx context.Context, c cid.Cid) error {
	s.deleted = append(s.deleted, c)
	return s.Blockstore.DeleteBlock(ctx, c)
}

func TestPeriodicGC(t *testing.T) {
	t.Parallel()

	gnd := mustTestNode(t, Config{
		Bitswap: true,
	})

	ctx := t.Context()

	cids := []cid.Cid{
		mustAddFile(t, gnd, []byte("a")),
		mustAddFile(t, gnd, []byte("b")),
		mustAddFile(t, gnd, []byte("c")),
		mustAddFile(t, gnd, []byte("d")),
		mustAddFile(t, gnd, []byte("e")),
		mustAddFile(t, gnd, []byte("f")),
	}

	for i, cid := range cids {
		has, err := gnd.blockstore.Has(ctx, cid)
		assert.NoError(t, err, i)
		assert.True(t, has, i)
	}

	// NOTE: ideally, we'd be able to spawn an isolated Rainbow instance with the
	// periodic GC settings configured (interval, threshold). The way it is now,
	// we can only test if the periodicGC function and the GC function work, but
	// not if the timer is being correctly set-up.
	//
	// Tracked in https://github.com/ipfs/rainbow/issues/89
	err := gnd.periodicGC(ctx, 1)
	require.NoError(t, err)

	for i, cid := range cids {
		has, err := gnd.blockstore.Has(ctx, cid)
		assert.NoError(t, err, i)
		assert.False(t, has, i)
	}
}

func TestGCDoesNotDeleteBlocksWithUnknownSize(t *testing.T) {
	inner, _, _ := newCapacityTestStore(t, 10)
	bad := blocks.NewBlock([]byte("bad"))
	good := blocks.NewBlock([]byte("ok"))
	require.NoError(t, inner.Put(t.Context(), bad))
	require.NoError(t, inner.Put(t.Context(), good))
	failing := &failingSizeBlockstore{Blockstore: inner, failing: bad.Cid()}
	node := &Node{blockstore: failing}

	require.NoError(t, node.GC(t.Context(), int64(len(good.RawData()))))
	require.Equal(t, []cid.Cid{good.Cid()}, failing.deleted)
	has, err := inner.Has(t.Context(), bad.Cid())
	require.NoError(t, err)
	require.True(t, has)
}
