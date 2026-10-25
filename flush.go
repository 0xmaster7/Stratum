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
	generations := make(map[string]uint64, len(paths)+1)
	seen := make(map[uint64]bool)
	largest := db.flushedWAL
	for _, path := range paths {
		gen, err := walGeneration(path)
		if err != nil {
			return err
		}
		if seen[gen] {
			return fmt.Errorf("duplicate WAL generation %d", gen)
		}
		seen[gen] = true
		generations[path] = gen
		if gen > largest {
			largest = gen
		}
	}
	if largest == ^uint64(0) {
		return errors.New("WAL generation exhausted")
	}
	legacy := filepath.Join(db.dir, "data.log")
	_, legacyErr := os.Stat(legacy)
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return legacyErr
	}
	var active string
	if legacyErr == nil {
		// Old revisions always put their newest writes in data.log.
		generations[legacy] = largest + 1
		paths = append(paths, legacy)
		active = legacy
	} else {
		for _, path := range paths {
			if generations[path] > db.flushedWAL && (active == "" || generations[path] > generations[active]) {
				active = path
			}
		}
		if active == "" {
			active = filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", largest+1))
			if largest == 0 && len(paths) == 0 {
				active = legacy
			} // Keep fresh legacy directories readable.
			generations[active] = largest + 1
			paths = append(paths, active)
		}
	}
	db.walGeneration = generations[active]
	sort.Slice(paths, func(i, j int) bool { return generations[paths[i]] < generations[paths[j]] })
	for _, path := range paths {
		if generations[path] <= db.flushedWAL {
			continue
		}
		flags := os.O_RDWR | os.O_APPEND
		if path == active {
			flags |= os.O_CREATE
		}
		f, err := os.OpenFile(path, flags, 0644)
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
		if path == active {
			db.wal = f
		} else {
			err = errors.Join(err, f.Close())
		}
		if err != nil {
			return err
		}
	}
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

// rotateLocked syncs the old WAL, then creates a uniquely named next generation.
// Its directory entry is synced before writers can use it. The empty file has
// no payload to sync: durable Put syncs its first record, and Flush/Close sync
// fast writes. Only legacy data.log needs a one-time rename and directory sync.
func (db *DB) rotateLocked() error {
	if db.frozen != nil || db.operations.Load() == 0 {
		return nil
	}
	fail := func(err error) error { db.writeErr = err; db.flushCond.Broadcast(); return err }
	if db.walErr != nil {
		return db.walErr
	}
	if db.walGeneration == ^uint64(0) {
		return fail(errors.New("WAL generation exhausted"))
	}
	if err := db.wal.Sync(); err != nil {
		return fail(err)
	}
	if filepath.Base(db.wal.Name()) == "data.log" {
		oldPath := filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", db.walGeneration))
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			return fail(fmt.Errorf("WAL segment already exists or cannot be inspected: %s", oldPath))
		}
		if err := os.Rename(db.wal.Name(), oldPath); err != nil {
			return fail(err)
		}
		if err := syncDir(db.dir); err != nil {
			return fail(err)
		}
	}
	nextGeneration := db.walGeneration + 1
	path := filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", nextGeneration))
	newWAL, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fail(err)
	}
	if err = syncDir(db.dir); err != nil {
		newWAL.Close()
		return fail(err)
	}
	oldWAL := db.wal
	db.wal = newWAL
	if err = oldWAL.Close(); err != nil {
		return fail(err)
	}
	db.frozen = &frozenMemtable{mem: db.mem, generation: db.walGeneration, operations: db.operations.Load()}
	db.walGeneration = nextGeneration
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
