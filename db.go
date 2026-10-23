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
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var ErrClosed = errors.New("database is closed")

type Entry struct {
	Key, Value string
	Deleted    bool
}
type IndexEntry struct {
	Key    string
	Offset int64
}
type sstable struct {
	data, indexData []byte
	bloom           *bloomFilter
	index           []IndexEntry
	count           int
	minKey, maxKey  string
	id              int
	path, indexPath string
}
type Options struct {
	CompactionInterval  time.Duration
	CompactionThreshold int
	CacheBytes          int
	FlushThreshold      int
	SyncWrites          bool
}
type DB struct {
	metrics                 counters
	stop, done              chan struct{}
	writeErr, backgroundErr error
	walErr                  error
	cache                   *readCache
	mu                      sync.RWMutex
	dir                     string
	opts                    Options
	mem                     *memTable
	tables                  []*sstable
	nextID                  int
	operations              atomic.Int64
	walMu                   sync.Mutex
	wal, lock               *os.File
	closed                  bool
}

func Open(dir string, opts Options) (_ *DB, err error) {
	started := time.Now()
	if opts.CompactionInterval == 0 {
		opts.CompactionInterval = 30 * time.Second
	}
	if opts.CompactionThreshold == 0 {
		opts.CompactionThreshold = 4
	}
	if opts.CompactionThreshold < 2 {
		return nil, errors.New("compaction threshold must be at least two")
	}
	if opts.CacheBytes == 0 {
		opts.CacheBytes = 16 << 20
	}
	if opts.CacheBytes < -1 {
		return nil, errors.New("cache bytes must be -1 (disabled), zero (default), or positive")
	}
	if opts.FlushThreshold == 0 {
		opts.FlushThreshold = 1000
	}
	if opts.FlushThreshold < 1 {
		return nil, errors.New("flush threshold must be positive")
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db := &DB{dir: dir, opts: opts, mem: newMemTable(), nextID: 1, cache: newCache(opts.CacheBytes), stop: make(chan struct{}), done: make(chan struct{})}
	db.lock, err = os.OpenFile(filepath.Join(dir, "LOCK"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(db.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		db.lock.Close()
		return nil, fmt.Errorf("database already open: %w", err)
	}
	defer func() {
		if err != nil {
			for _, t := range db.tables {
				t.close()
			}
			if db.wal != nil {
				db.wal.Close()
			}
			db.lock.Close()
		}
	}()
	paths, err := filepath.Glob(filepath.Join(dir, "sstable_*.log"))
	if err != nil {
		return nil, err
	}
	active, hasManifest, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		id, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "sstable_"), ".log"))
		if e != nil || id < 1 {
			return nil, fmt.Errorf("invalid SSTable name %s", path)
		}
		if id >= db.nextID {
			db.nextID = id + 1
		}
		if hasManifest && !active[id] {
			continue
		}
		t := &sstable{id: id, path: path, indexPath: strings.TrimSuffix(path, ".log") + ".index"}
		// Rebuilding the sparse index also validates existing records before serving reads.
		if err = t.rebuildIndex(); err != nil {
			return nil, err
		}
		if err = t.mapFiles(); err != nil {
			return nil, err
		}
		db.tables = append(db.tables, t)
		delete(active, id)
		if id >= db.nextID {
			db.nextID = id + 1
		}
	}
	if len(active) > 0 {
		return nil, errors.New("manifest references missing SSTable")
	}
	sort.Slice(db.tables, func(i, j int) bool { return db.tables[i].id < db.tables[j].id })
	db.wal, err = os.OpenFile(filepath.Join(dir, "data.log"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	recoveryStarted := time.Now()
	r := bufio.NewReader(db.wal)
	var valid int64
	for {
		e, n, readErr := readRecord(r)
		if readErr == io.EOF {
			break
		}
		if readErr == io.ErrUnexpectedEOF {
			if err = db.wal.Truncate(valid); err != nil {
				return nil, err
			}
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("WAL recovery: %w", readErr)
		}
		valid += n
		db.mem.shard(e.Key).put(e)
		db.operations.Add(1)
	}
	db.metrics.recoveryOps.Store(uint64(db.operations.Load()))
	db.metrics.recoveryNanos.Store(uint64(time.Since(recoveryStarted)))
	if !hasManifest {
		if err = db.publish(db.tables); err != nil {
			return nil, err
		}
	}
	db.metrics.startupNanos.Store(uint64(time.Since(started)))
	go db.runCompactor()
	return db, nil
}
func (db *DB) Put(key, value string) error {
	err := db.mutate(Entry{Key: key, Value: value})
	if err == nil {
		db.metrics.puts.Add(1)
	}
	return err
}
func (db *DB) Delete(key string) error {
	err := db.mutate(Entry{Key: key, Deleted: true})
	if err == nil {
		db.metrics.deletes.Add(1)
	}
	return err
}
func (db *DB) mutate(e Entry) error {
	db.mu.RLock()
	if db.closed {
		db.mu.RUnlock()
		return ErrClosed
	}
	if db.writeErr != nil {
		err := db.writeErr
		db.mu.RUnlock()
		return err
	}
	if len(e.Key)+len(e.Value) > maxRecordSize {
		db.mu.RUnlock()
		return errors.New("record exceeds 64 MiB limit")
	}
	shard := db.mem.shard(e.Key)
	shard.mu.Lock()
	db.walMu.Lock()
	var err error
	if db.walErr != nil {
		err = db.walErr
	} else {
		var n int64
		n, err = writeRecord(db.wal, e)
		db.metrics.walBytes.Add(uint64(n))
	}
	if err == nil && db.opts.SyncWrites {
		err = db.wal.Sync()
	}
	if err != nil {
		db.walErr = err
	}
	if err == nil {
		shard.put(e)
		db.cache.remove(e.Key)
		db.operations.Add(1)
	}
	flush := db.operations.Load() >= int64(db.opts.FlushThreshold)
	db.walMu.Unlock()
	shard.mu.Unlock()
	db.mu.RUnlock()
	if err != nil {
		return err
	}
	if flush {
		return db.Flush()
	}
	return nil
}
func (db *DB) Get(key string) (value string, exists bool, readErr error) {
	defer func() {
		if readErr == nil && !exists {
			db.metrics.misses.Add(1)
		}
	}()
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return "", false, ErrClosed
	}
	db.metrics.gets.Add(1)
	if e, ok := db.mem.get(key); ok {
		db.metrics.memHits.Add(1)
		return e.Value, !e.Deleted, nil
	}
	if value, ok := db.cache.get(key); ok {
		db.metrics.cacheHits.Add(1)
		return value, true, nil
	}
	for i := len(db.tables) - 1; i >= 0; i-- {
		t := db.tables[i]
		db.metrics.tableChecks.Add(1)
		if t.count == 0 || key < t.minKey || key > t.maxKey {
			continue
		}
		db.metrics.bloomChecks.Add(1)
		if !t.bloom.contains(key) {
			db.metrics.bloomSkips.Add(1)
			continue
		}
		db.metrics.tableReads.Add(1)
		e, ok, err := t.get(key, &db.metrics)
		if err != nil {
			return "", false, err
		}
		if ok {
			if !e.Deleted {
				db.cache.put(key, e.Value)
			}
			return e.Value, !e.Deleted, nil
		}
	}
	return "", false, nil
}
func (db *DB) Flush() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	return db.flushLocked()
}
func (db *DB) flushLocked() error {
	if db.writeErr != nil {
		return db.writeErr
	}
	if db.walErr != nil {
		return db.walErr
	}
	if db.operations.Load() == 0 {
		return nil
	}
	started := time.Now()
	entries := db.mem.entries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	t, err := db.writeTable(entries)
	if err != nil {
		return err
	}
	tables := append(append([]*sstable(nil), db.tables...), t)
	if err = db.install(tables); err != nil {
		t.close()
		return err
	}
	if err = db.wal.Truncate(0); err != nil {
		db.writeErr = err
		return err
	}
	if err = db.wal.Sync(); err != nil {
		db.writeErr = err
		return err
	}
	db.mem = newMemTable()
	db.cache.clear()
	db.operations.Store(0)
	db.metrics.flushes.Add(1)
	db.metrics.flushNanos.Add(uint64(time.Since(started)))
	return nil
}
func (db *DB) writeTable(entries []Entry) (*sstable, error) {
	i := 0
	return db.writeTableIterator(func() (Entry, bool, error) {
		if i == len(entries) {
			return Entry{}, false, nil
		}
		e := entries[i]
		i++
		return e, true, nil
	})
}
func (db *DB) writeTableIterator(next func() (Entry, bool, error)) (*sstable, error) {
	id := db.nextID
	db.nextID++
	path := filepath.Join(db.dir, fmt.Sprintf("sstable_%d.log", id))
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path + ".tmp")
	w := bufio.NewWriter(f)
	for {
		e, ok, nextErr := next()
		if nextErr != nil {
			f.Close()
			return nil, nextErr
		}
		if !ok {
			break
		}
		if _, err = writeRecord(w, e); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err = w.Flush(); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	t := &sstable{id: id, path: path, indexPath: strings.TrimSuffix(path, ".log") + ".index"}
	// Publish only complete, synced data. A crash before WAL truncation is safe to replay.
	if err = os.Rename(path+".tmp", path); err != nil {
		return nil, err
	}
	if err = t.rebuildIndex(); err != nil {
		return nil, err
	}
	if err = syncDir(db.dir); err != nil {
		return nil, err
	}
	if err = t.mapFiles(); err != nil {
		return nil, err
	}
	return t, nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	close(db.stop)
	var err error
	for _, t := range db.tables {
		if e := t.close(); err == nil {
			err = e
		}
	}
	if e := db.wal.Sync(); err == nil {
		err = e
	}
	if e := db.wal.Close(); err == nil {
		err = e
	}
	if e := db.lock.Close(); err == nil {
		err = e
	}
	db.mu.Unlock()
	<-db.done
	return err
}
func (t *sstable) rebuildIndex() error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	defer f.Close()
	index, err := os.Create(t.indexPath + ".tmp")
	if err != nil {
		return err
	}
	defer os.Remove(t.indexPath + ".tmp")
	t.index = nil
	var hashes []uint64
	r := bufio.NewReader(f)
	offset := int64(0)
	count := 0
	last := ""
	for {
		e, n, err := readRecord(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			index.Close()
			return err
		}
		if count > 0 && e.Key <= last {
			index.Close()
			return errors.New("unsorted SSTable")
		}
		if count == 0 {
			t.minKey = e.Key
		}
		t.maxKey = e.Key
		if count%10 == 0 {
			t.index = append(t.index, IndexEntry{e.Key, offset})
			if _, err = writeIndex(index, e.Key, offset); err != nil {
				index.Close()
				return err
			}
		}
		hashes = append(hashes, hash(e.Key))
		offset += n
		last = e.Key
		count++
	}
	t.count = count
	t.bloom = newBloom(count)
	for _, h := range hashes {
		t.bloom.addHash(h)
	}
	if err = index.Sync(); err != nil {
		index.Close()
		return err
	}
	if err = index.Close(); err != nil {
		return err
	}
	return os.Rename(t.indexPath+".tmp", t.indexPath)
}
func (t *sstable) get(key string, metrics *counters) (Entry, bool, error) {
	if t.count == 0 || key < t.minKey || key > t.maxKey {
		return Entry{}, false, nil
	}
	entries := t.index
	i := indexBefore(entries, key)
	if i < 0 {
		return Entry{}, false, nil
	}
	offset := entries[i].Offset
	for {
		e, n, err := recordAt(t.data, offset)
		if err == nil {
			metrics.recordsRead.Add(1)
			metrics.readBytes.Add(uint64(n))
		}
		if err == io.EOF {
			return Entry{}, false, nil
		}
		if err != nil {
			return Entry{}, false, err
		}
		if e.Key == key {
			return e, true, nil
		}
		if e.Key > key {
			return Entry{}, false, nil
		}
		offset += n
	}
}

func indexBefore(entries []IndexEntry, key string) int {
	return sort.Search(len(entries), func(i int) bool { return entries[i].Key > key }) - 1
}
