package main

import (
	"container/list"
	"sync"
	"time"
)

const (
	providerCacheCapacity = 256
	providerCacheTTL      = 15 * time.Second
)

type providerRecord struct {
	PeerID    string
	Addresses []string
}

type providerCacheEntry struct {
	key     string
	results []providerRecord
	expires time.Time
}

type providerCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
}

func newProviderCache() *providerCache {
	return &providerCache{
		entries: make(map[string]*list.Element, providerCacheCapacity),
		lru:     list.New(),
	}
}

func (c *providerCache) get(key string, now time.Time) ([]providerRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := element.Value.(*providerCacheEntry)
	if !now.Before(entry.expires) {
		delete(c.entries, key)
		c.lru.Remove(element)
		return nil, false
	}
	c.lru.MoveToFront(element)
	return cloneProviderRecords(entry.results), true
}

func (c *providerCache) put(key string, results []providerRecord, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	results = cloneProviderRecords(results)
	if element, ok := c.entries[key]; ok {
		element.Value.(*providerCacheEntry).results = results
		element.Value.(*providerCacheEntry).expires = now.Add(providerCacheTTL)
		c.lru.MoveToFront(element)
		return
	}

	element := c.lru.PushFront(&providerCacheEntry{
		key:     key,
		results: results,
		expires: now.Add(providerCacheTTL),
	})
	c.entries[key] = element
	if c.lru.Len() > providerCacheCapacity {
		oldest := c.lru.Back()
		delete(c.entries, oldest.Value.(*providerCacheEntry).key)
		c.lru.Remove(oldest)
	}
}

func cloneProviderRecords(results []providerRecord) []providerRecord {
	if results == nil {
		return nil
	}
	cloned := make([]providerRecord, 0, len(results))
	for _, result := range results {
		bounded, ok := boundedProviderRecord(result)
		if !ok {
			continue
		}
		cloned = append(cloned, providerRecord{PeerID: bounded.PeerID, Addresses: append([]string(nil), bounded.Addresses...)})
	}
	return cloned
}
