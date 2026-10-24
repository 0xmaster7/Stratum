package main

import (
	"sort"
	"sync"
	"sync/atomic"
)

const shardCount = 256

type memShard struct {
	mu      sync.RWMutex
	buckets [][]Entry
	count   int
}
type memTable struct {
	shards         [shardCount]memShard
	revision       atomic.Uint64
	sortedRevision uint64
	sorted         []Entry // Accessed only while holding the database's exclusive lock.
}

func hash(key string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return h
}
func newMemTable() *memTable {
	m := &memTable{}
	for i := range m.shards {
		m.shards[i].buckets = make([][]Entry, 4)
	}
	return m
}
func (m *memTable) shard(key string) *memShard { return &m.shards[hash(key)%shardCount] }
func (s *memShard) put(e Entry) {
	b := int((hash(e.Key) / shardCount) % uint64(len(s.buckets)))
	for i := range s.buckets[b] {
		if s.buckets[b][i].Key == e.Key {
			s.buckets[b][i] = e
			return
		}
	}
	s.buckets[b] = append(s.buckets[b], e)
	s.count++
	if s.count*4 > len(s.buckets)*3 {
		buckets := make([][]Entry, len(s.buckets)*2)
		for _, bucket := range s.buckets {
			for _, e := range bucket {
				b := int((hash(e.Key) / shardCount) % uint64(len(buckets)))
				buckets[b] = append(buckets[b], e)
			}
		}
		s.buckets = buckets
	}
}
func (m *memTable) get(key string) (Entry, bool) {
	s := m.shard(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := int((hash(key) / shardCount) % uint64(len(s.buckets)))
	for _, e := range s.buckets[b] {
		if e.Key == key {
			return e, true
		}
	}
	return Entry{}, false
}

// Called with the database's exclusive lock, after active operations have completed.
func (m *memTable) entries() []Entry {
	var entries []Entry
	for i := range m.shards {
		for _, bucket := range m.shards[i].buckets {
			entries = append(entries, bucket...)
		}
	}
	return entries
}

// sortedEntries reuses an immutable view until the next mutation. The caller
// holds db.mu exclusively, excluding mutations and other snapshot builders.
func (m *memTable) sortedEntries() []Entry {
	version := m.revision.Load()
	if m.sorted == nil || version != m.sortedRevision {
		m.sorted = m.entries()
		sort.Slice(m.sorted, func(i, j int) bool { return m.sorted[i].Key < m.sorted[j].Key })
		m.sortedRevision = version
	}
	return m.sorted
}
