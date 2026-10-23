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
	bloom           *bloomFilter
	index           []IndexEntry
	count           int
	minKey, maxKey  string
	id              int
	path, indexPath string
}
type Options struct {
	FlushThreshold int
	SyncWrites     bool
}
type DB struct {
	mu         sync.RWMutex
	dir        string
	opts       Options
	mem        *memTable
	tables     []*sstable
	nextID     int
	operations atomic.Int64
	walMu      sync.Mutex
	wal, lock  *os.File
	closed     bool
}

func Open(dir string, opts Options) (_ *DB, err error) {
	if opts.FlushThreshold == 0 {
		opts.FlushThreshold = 1000
	}
	if opts.FlushThreshold < 1 {
		return nil, errors.New("flush threshold must be positive")
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db := &DB{dir: dir, opts: opts, mem: newMemTable(), nextID: 1}
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
	for _, path := range paths {
		id, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "sstable_"), ".log"))
		if e != nil || id < 1 {
			return nil, fmt.Errorf("invalid SSTable name %s", path)
		}
		t := &sstable{id: id, path: path, indexPath: strings.TrimSuffix(path, ".log") + ".index"}
		// Rebuilding the sparse index also validates existing records before serving reads.
		if err = t.rebuildIndex(); err != nil {
			return nil, err
		}
		db.tables = append(db.tables, t)
		if id >= db.nextID {
			db.nextID = id + 1
		}
	}
	sort.Slice(db.tables, func(i, j int) bool { return db.tables[i].id < db.tables[j].id })
	db.wal, err = os.OpenFile(filepath.Join(dir, "data.log"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
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
	return db, nil
}
func (db *DB) Put(key, value string) error { return db.mutate(Entry{Key: key, Value: value}) }
func (db *DB) Delete(key string) error     { return db.mutate(Entry{Key: key, Deleted: true}) }
func (db *DB) mutate(e Entry) error {
	db.mu.RLock()
	if db.closed {
		db.mu.RUnlock()
		return ErrClosed
	}
	if len(e.Key)+len(e.Value) > maxRecordSize {
		db.mu.RUnlock()
		return errors.New("record exceeds 64 MiB limit")
	}
	shard := db.mem.shard(e.Key)
	shard.mu.Lock()
	db.walMu.Lock()
	_, err := writeRecord(db.wal, e)
	if err == nil && db.opts.SyncWrites {
		err = db.wal.Sync()
	}
	if err == nil {
		shard.put(e)
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
func (db *DB) Get(key string) (string, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return "", false, ErrClosed
	}
	if e, ok := db.mem.get(key); ok {
		return e.Value, !e.Deleted, nil
	}
	for i := len(db.tables) - 1; i >= 0; i-- {
		e, ok, err := db.tables[i].get(key)
		if err != nil {
			return "", false, err
		}
		if ok {
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
	if db.operations.Load() == 0 {
		return nil
	}
	entries := db.mem.entries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	t, err := db.writeTable(entries)
	if err != nil {
		return err
	}
	db.tables = append(db.tables, t)
	if err = db.wal.Truncate(0); err != nil {
		return err
	}
	if err = db.wal.Sync(); err != nil {
		return err
	}
	db.mem = newMemTable()
	db.operations.Store(0)
	return nil
}
func (db *DB) writeTable(entries []Entry) (*sstable, error) {
	id := db.nextID
	db.nextID++
	path := filepath.Join(db.dir, fmt.Sprintf("sstable_%d.log", id))
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path + ".tmp")
	w := bufio.NewWriter(f)
	for _, e := range entries {
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
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	err := db.wal.Sync()
	if e := db.wal.Close(); err == nil {
		err = e
	}
	if e := db.lock.Close(); err == nil {
		err = e
	}
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
func (t *sstable) get(key string) (Entry, bool, error) {
	if t.count == 0 || key < t.minKey || key > t.maxKey || !t.bloom.contains(key) {
		return Entry{}, false, nil
	}
	entries := t.index
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Key > key }) - 1
	if i < 0 {
		return Entry{}, false, nil
	}
	f, err := os.Open(t.path)
	if err != nil {
		return Entry{}, false, err
	}
	defer f.Close()
	if _, err = f.Seek(entries[i].Offset, io.SeekStart); err != nil {
		return Entry{}, false, err
	}
	r := bufio.NewReader(f)
	for {
		e, _, err := readRecord(r)
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
	}
}
