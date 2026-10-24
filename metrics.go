package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

type counters struct {
	puts, deletes, gets, misses, memHits, cacheHits                                    atomic.Uint64
	bloomChecks, bloomSkips, tableChecks, tableReads, recordsRead, readBytes, walBytes atomic.Uint64
	flushes, flushNanos, compactions, compactionNanos, scans, scanNanos                atomic.Uint64
	recoveryOps, recoveryNanos, startupNanos                                           atomic.Uint64
}

// Metrics are process-local cumulative counters; gauges describe the current view.
// LogicalReadBytes counts decoded SSTable records, not physical device traffic.
type Metrics struct {
	Puts, Deletes, Gets, Misses, MemtableHits, CacheHits                                                         uint64
	BloomChecks, BloomSkips, TableChecks, TableReads, RecordsRead, LogicalReadBytes, WALBytesWritten             uint64
	Flushes, FlushNanos, Compactions, CompactionNanos, Scans, ScanNanos                                          uint64
	RecoveryOperations, RecoveryNanos, StartupNanos                                                              uint64
	SSTables, MemtableEntries, PendingOperations, CacheBytes, CacheBudget, MappedBytes, BloomBytes, IndexEntries int64
	DiskBytes                                                                                                    int64
	BackgroundError, WriteError                                                                                  string
}

func (db *DB) Stats() Metrics {
	db.mu.RLock()
	defer db.mu.RUnlock()
	c := &db.metrics
	m := Metrics{Puts: c.puts.Load(), Deletes: c.deletes.Load(), Gets: c.gets.Load(), Misses: c.misses.Load(), MemtableHits: c.memHits.Load(), CacheHits: c.cacheHits.Load(), BloomChecks: c.bloomChecks.Load(), BloomSkips: c.bloomSkips.Load(), TableChecks: c.tableChecks.Load(), TableReads: c.tableReads.Load(), RecordsRead: c.recordsRead.Load(), LogicalReadBytes: c.readBytes.Load(), WALBytesWritten: c.walBytes.Load(), Flushes: c.flushes.Load(), FlushNanos: c.flushNanos.Load(), Compactions: c.compactions.Load(), CompactionNanos: c.compactionNanos.Load(), Scans: c.scans.Load(), ScanNanos: c.scanNanos.Load(), RecoveryOperations: c.recoveryOps.Load(), RecoveryNanos: c.recoveryNanos.Load(), StartupNanos: c.startupNanos.Load(), SSTables: int64(len(db.tables)), PendingOperations: db.operations.Load()}
	memtables := []*memTable{db.mem}
	if db.frozen != nil {
		memtables = append(memtables, db.frozen.mem)
		m.PendingOperations += db.frozen.operations
	}
	for _, mem := range memtables {
		for i := range mem.shards {
			s := &mem.shards[i]
			s.mu.RLock()
			m.MemtableEntries += int64(s.count)
			s.mu.RUnlock()
		}
	}
	db.cache.mu.Lock()
	m.CacheBytes = int64(db.cache.used)
	m.CacheBudget = int64(db.cache.budget)
	db.cache.mu.Unlock()
	for _, t := range db.tables {
		m.MappedBytes += int64(len(t.data) + len(t.indexData))
		m.BloomBytes += int64(len(t.bloom.words) * 8)
		m.IndexEntries += int64(len(t.index))
		for _, p := range []string{t.path, t.indexPath} {
			if st, err := os.Stat(p); err == nil {
				m.DiskBytes += st.Size()
			}
		}
	}
	walPaths, _ := filepath.Glob(filepath.Join(db.dir, "wal_*.log"))
	paths := append(walPaths, filepath.Join(db.dir, "data.log"), filepath.Join(db.dir, "MANIFEST"))
	for _, path := range paths {
		if st, err := os.Stat(path); err == nil {
			m.DiskBytes += st.Size()
		}
	}
	if db.backgroundErr != nil {
		m.BackgroundError = db.backgroundErr.Error()
	}
	if db.writeErr != nil {
		m.WriteError = db.writeErr.Error()
	}
	db.walMu.Lock()
	if db.walErr != nil {
		m.WriteError = db.walErr.Error()
	}
	db.walMu.Unlock()
	return m
}
