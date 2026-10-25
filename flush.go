package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type frozenMemtable struct {
	mem        *memTable
	generation uint64
	operations int64
}

func walGeneration(path string) (uint64, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "wal_"), ".log")
	n, err := strconv.ParseUint(name, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("invalid WAL segment %s", path)
	}
	return n, nil
}

func (db *DB) recoverWALs() error {
	paths, err := filepath.Glob(filepath.Join(db.dir, "wal_*.log"))
	if err != nil {
		return err
	}
	generations := make(map[string]uint64, len(paths))
	db.walGeneration = db.flushedWAL + 1
	for _, path := range paths {
		gen, err := walGeneration(path)
		if err != nil {
			return err
		}
		generations[path] = gen
		if gen >= db.walGeneration {
			db.walGeneration = gen + 1
		}
	}
	sort.Slice(paths, func(i, j int) bool { return generations[paths[i]] < generations[paths[j]] })
	paths = append(paths, filepath.Join(db.dir, "data.log"))
	for _, path := range paths {
		if gen, ok := generations[path]; ok && gen <= db.flushedWAL {
			continue
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		r := bufio.NewReader(f)
		var valid int64
		for {
			e, n, readErr := readRecord(r)
			if readErr == io.EOF {
				break
			}
			if readErr == io.ErrUnexpectedEOF {
				err = f.Truncate(valid)
				break
			}
			if readErr != nil {
				err = fmt.Errorf("WAL recovery %s: %w", path, readErr)
				break
			}
			valid += n
			db.mem.shard(e.Key).put(e)
			db.operations.Add(1)
		}
		if path == filepath.Join(db.dir, "data.log") {
			db.wal = f
		} else {
			err = errors.Join(err, f.Close())
		}
		if err != nil {
			return err
		}
	}
	// Persist a newly created active WAL name before durable mutations can return.
	if err := db.wal.Sync(); err != nil {
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return err
	}
	return db.removeFlushedWALs()
}

func (db *DB) removeFlushedWALs() error {
	paths, err := filepath.Glob(filepath.Join(db.dir, "wal_*.log"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		gen, err := walGeneration(path)
		if err != nil {
			return err
		}
		if gen <= db.flushedWAL {
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	// Deletion need not be crash-durable: the synced manifest checkpoint makes
	// a reappearing segment obsolete, and recovery removes it without replay.
	return nil
}

// rotateLocked is a short publication boundary: all mutations are excluded.
// The old WAL is durable before its name changes. Recovery replays every segment
// newer than the manifest checkpoint, then data.log, in that order.
func (db *DB) rotateLocked() error {
	if db.frozen != nil || db.operations.Load() == 0 {
		return nil
	}
	fail := func(err error) error { db.writeErr = err; db.flushCond.Broadcast(); return err }
	if db.walErr != nil {
		return db.walErr
	}
	if err := db.wal.Sync(); err != nil {
		return fail(err)
	}
	path := filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", db.walGeneration))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fail(fmt.Errorf("WAL segment already exists or cannot be inspected: %s", path))
	}
	if err := os.Rename(filepath.Join(db.dir, "data.log"), path); err != nil {
		return fail(err)
	}
	// Make the rename durable before a new active WAL can acknowledge writes.
	if err := syncDir(db.dir); err != nil {
		return fail(err)
	}
	newWAL, err := os.OpenFile(filepath.Join(db.dir, "data.log"), os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fail(err)
	}
	if err = newWAL.Sync(); err == nil {
		err = syncDir(db.dir)
	}
	if err != nil {
		newWAL.Close()
		return fail(err)
	}
	oldWAL := db.wal
	db.wal = newWAL
	if err = oldWAL.Close(); err != nil {
		return fail(err)
	}
	db.frozen = &frozenMemtable{mem: db.mem, generation: db.walGeneration, operations: db.operations.Load()}
	db.walGeneration++
	db.mem = newMemTable()
	db.operations.Store(0)
	return nil
}

// At most one immutable memtable is queued. Writers apply backpressure once the
// replacement fills, instead of allowing an unbounded background work queue.
func (db *DB) maybeFlush() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for !db.closed && db.writeErr == nil && db.frozen != nil && db.operations.Load() >= int64(db.opts.FlushThreshold) {
		db.flushCond.Wait()
	}
	if db.closed {
		return ErrClosed
	}
	if db.writeErr != nil {
		return db.writeErr
	}
	if db.operations.Load() < int64(db.opts.FlushThreshold) {
		return nil
	}
	if err := db.rotateLocked(); err != nil {
		return err
	}
	select {
	case db.flushWake <- struct{}{}:
	default:
	}
	return nil
}

func (db *DB) Flush() error {
	db.maintenanceMu.Lock()
	defer db.maintenanceMu.Unlock()
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrClosed
	}
	target := db.flushedWAL
	if db.frozen != nil {
		target = db.frozen.generation
	}
	if db.operations.Load() > 0 {
		target = db.walGeneration
	}
	db.mu.Unlock()
	return db.flushTo(target, nil)
}

// Called with maintenanceMu held. afterSnapshot is a deterministic test seam.
func (db *DB) flushTo(target uint64, afterSnapshot func()) error {
	for {
		db.mu.Lock()
		if db.writeErr != nil {
			err := db.writeErr
			db.mu.Unlock()
			return err
		}
		if db.walErr != nil {
			err := db.walErr
			db.mu.Unlock()
			return err
		}
		if db.flushedWAL >= target {
			db.mu.Unlock()
			return nil
		}
		if err := db.rotateLocked(); err != nil {
			db.mu.Unlock()
			return err
		}
		frozen := db.frozen
		if frozen == nil {
			db.mu.Unlock()
			return errors.New("missing pending WAL generation")
		}
		db.mu.Unlock()
		if afterSnapshot != nil {
			afterSnapshot()
		}
		started := time.Now()
		entries := frozen.mem.entries()
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		table, err := db.writeTable(entries)
		db.mu.Lock()
		if err == nil {
			previous := db.flushedWAL
			db.flushedWAL = frozen.generation
			err = db.install(append(append([]*sstable(nil), db.tables...), table))
			if err != nil {
				db.flushedWAL = previous
				table.close()
			}
		}
		if err != nil {
			db.writeErr = err
			db.flushCond.Broadcast()
			db.mu.Unlock()
			return err
		}
		db.frozen = nil
		db.flushCond.Broadcast()
		db.mu.Unlock()
		db.metrics.flushes.Add(1)
		db.metrics.flushNanos.Add(uint64(time.Since(started)))
		if err = db.removeFlushedWALs(); err != nil {
			db.mu.Lock()
			db.writeErr = err
			db.flushCond.Broadcast()
			db.mu.Unlock()
			return err
		}
	}
}

func (db *DB) flushPending() error {
	db.maintenanceMu.Lock()
	defer db.maintenanceMu.Unlock()
	db.mu.RLock()
	if db.closed {
		db.mu.RUnlock()
		return ErrClosed
	}
	target := db.flushedWAL
	if db.frozen != nil {
		target = db.frozen.generation
	}
	db.mu.RUnlock()
	return db.flushTo(target, nil)
}
