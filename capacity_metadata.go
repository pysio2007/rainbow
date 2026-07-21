package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	datastore "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

const (
	capacityMetadataKey      = "RAINBOW_CAPACITY_METADATA_V1"
	capacityMetadataEntryKey = "CAPACITY_ENTRY_"
	capacityMetadataSchema   = 2
)

// capacityMetadata is retained as the decoding shape for recovery tests and old
// snapshot records. New records use capacityMetadataState and entries.
type capacityMetadata struct {
	Schema int              `json:"schema"`
	Dirty  bool             `json:"dirty"`
	Total  int64            `json:"total"`
	Order  []string         `json:"order"`
	Sizes  map[string]int64 `json:"sizes"`
}

type capacityMetadataState struct {
	Schema  int    `json:"schema"`
	Dirty   bool   `json:"dirty"`
	Total   int64  `json:"total"`
	NextSeq uint64 `json:"next_seq"`
}

type capacityEntry struct {
	Seq  uint64
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

type persistedCapacityEntry struct {
	Seq  uint64 `json:"seq"`
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

type capacityMetadataStore struct {
	ds      datastore.Batching
	entries map[uint64]capacityEntry
	stale   []datastore.Key
}

func newCapacityMetadataStore(ds datastore.Batching) *capacityMetadataStore {
	return &capacityMetadataStore{ds: ds, entries: make(map[uint64]capacityEntry)}
}

func (m *capacityMetadataStore) load(ctx context.Context) (capacityMetadataState, bool, error) {
	state := capacityMetadataState{Schema: capacityMetadataSchema, Dirty: true, NextSeq: 1}
	needsRepair := false
	data, err := m.ds.Get(ctx, datastore.NewKey(capacityMetadataKey))
	if err == nil {
		if json.Unmarshal(data, &state) != nil || state.Schema != capacityMetadataSchema || state.NextSeq == 0 {
			state = capacityMetadataState{Schema: capacityMetadataSchema, Dirty: true, NextSeq: 1}
			needsRepair = true
		}
	} else if err != datastore.ErrNotFound {
		return state, true, err
	}
	results, err := m.ds.Query(ctx, query.Query{})
	if err != nil {
		return state, true, err
	}
	defer results.Close()
	for {
		entry, ok := results.NextSync()
		if !ok {
			break
		}
		if entry.Error != nil {
			return state, true, entry.Error
		}
		if !strings.HasPrefix(entry.Key, "/"+capacityMetadataEntryKey) {
			continue
		}
		seqText := strings.TrimPrefix(entry.Key, "/"+capacityMetadataEntryKey)
		seq, parseErr := strconv.ParseUint(seqText, 10, 64)
		var stored persistedCapacityEntry
		decodeErr := json.Unmarshal(entry.Value, &stored)
		keyBytes, keyErr := base64.RawStdEncoding.DecodeString(stored.Key)
		if parseErr != nil || decodeErr != nil || keyErr != nil || len(keyBytes) == 0 || stored.Size < 0 {
			needsRepair = true
			m.stale = append(m.stale, datastore.NewKey(entry.Key))
			continue
		}
		record := capacityEntry{Seq: seq, Key: string(keyBytes), Size: stored.Size}
		if _, exists := m.entries[seq]; exists {
			needsRepair = true
			continue
		}
		m.entries[seq] = record
		if seq == capacityMaxSequence {
			needsRepair = true
		} else if seq >= state.NextSeq {
			state.NextSeq = seq + 1
		}
	}
	if needsRepair {
		state.Dirty = true
	}
	return state, needsRepair, nil
}

func (m *capacityMetadataStore) writeState(ctx context.Context, state capacityMetadataState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return m.ds.Put(ctx, datastore.NewKey(capacityMetadataKey), data)
}

func (m *capacityMetadataStore) markDirty(ctx context.Context, state *capacityMetadataState) error {
	state.Schema = capacityMetadataSchema
	state.Dirty = true
	if state.NextSeq == 0 {
		state.NextSeq = 1
	}
	return m.writeState(ctx, *state)
}

func capacityEntryKey(seq uint64) datastore.Key {
	return datastore.NewKey(fmt.Sprintf("%s%020d", capacityMetadataEntryKey, seq))
}

func (m *capacityMetadataStore) replace(ctx context.Context, order []capacityEntry, state *capacityMetadataState) error {
	desired := make(map[uint64]capacityEntry, len(order))
	for _, entry := range order {
		desired[entry.Seq] = entry
	}
	for seq := range m.entries {
		if _, ok := desired[seq]; !ok {
			if err := m.ds.Delete(ctx, capacityEntryKey(seq)); err != nil {
				return err
			}
		}
	}
	for _, key := range m.stale {
		if err := m.ds.Delete(ctx, key); err != nil {
			return err
		}
	}
	for _, entry := range order {
		data, err := json.Marshal(persistedCapacityEntry{
			Seq: entry.Seq, Key: base64.RawStdEncoding.EncodeToString([]byte(entry.Key)), Size: entry.Size,
		})
		if err != nil {
			return err
		}
		if err := m.ds.Put(ctx, capacityEntryKey(entry.Seq), data); err != nil {
			return err
		}
	}
	state.Schema = capacityMetadataSchema
	state.Dirty = false
	if state.NextSeq == 0 {
		state.NextSeq = 1
	}
	if err := m.writeState(ctx, *state); err != nil {
		return err
	}
	m.entries = desired
	m.stale = nil
	return nil
}

func (m *capacityMetadataStore) commitDelta(ctx context.Context, added, removed []capacityEntry, state *capacityMetadataState) error {
	for _, entry := range removed {
		if err := m.ds.Delete(ctx, capacityEntryKey(entry.Seq)); err != nil {
			return err
		}
	}
	for _, entry := range added {
		data, err := json.Marshal(persistedCapacityEntry{
			Seq: entry.Seq, Key: base64.RawStdEncoding.EncodeToString([]byte(entry.Key)), Size: entry.Size,
		})
		if err != nil {
			return err
		}
		if err := m.ds.Put(ctx, capacityEntryKey(entry.Seq), data); err != nil {
			return err
		}
	}
	state.Schema = capacityMetadataSchema
	state.Dirty = false
	if err := m.writeState(ctx, *state); err != nil {
		return err
	}
	for _, entry := range removed {
		delete(m.entries, entry.Seq)
	}
	for _, entry := range added {
		m.entries[entry.Seq] = entry
	}
	return nil
}
