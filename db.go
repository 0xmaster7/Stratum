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
	id              int
	path, indexPath string
}
type Options struct {
	FlushThreshold int
	SyncWrites     bool
}
type DB struct {
	mu                 sync.RWMutex
	dir                string
	opts               Options
	mem                map[string]Entry
	tables             []*sstable
	nextID, operations int
	wal, lock          *os.File
	closed             bool
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
	db := &DB{dir: dir, opts: opts, mem: make(map[string]Entry), nextID: 1}
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
		db.mem[e.Key] = e
		db.operations++
	}
	return db, nil
}
func (db *DB) Put(key, value string) error { return db.mutate(Entry{Key: key, Value: value}) }
func (db *DB) Delete(key string) error     { return db.mutate(Entry{Key: key, Deleted: true}) }
func (db *DB) mutate(e Entry) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	if strings.ContainsAny(e.Key, " \t\r\n") || e.Key == "" || strings.ContainsAny(e.Value, "\r\n") {
		return errors.New("text records require a nonempty whitespace-free key and a single-line value")
	}
	if _, err := writeRecord(db.wal, e); err != nil {
		return err
	}
	if db.opts.SyncWrites {
		if err := db.wal.Sync(); err != nil {
			return err
		}
	}
	db.mem[e.Key] = e
	db.operations++
	if db.operations >= db.opts.FlushThreshold {
		return db.flushLocked()
	}
	return nil
}
func (db *DB) Get(key string) (string, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return "", false, ErrClosed
	}
	if e, ok := db.mem[key]; ok {
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
	if db.operations == 0 {
		return nil
	}
	entries := make([]Entry, 0, len(db.mem))
	for _, e := range db.mem {
		entries = append(entries, e)
	}
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
	db.mem = make(map[string]Entry)
	db.operations = 0
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
func writeRecord(w io.Writer, e Entry) (int64, error) {
	var s string
	if e.Deleted {
		s = "DELETE " + e.Key + "\n"
	} else {
		s = "PUT " + e.Key + " " + e.Value + "\n"
	}
	n, err := io.WriteString(w, s)
	return int64(n), err
}
func readRecord(r *bufio.Reader) (Entry, int64, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(s) > 0 {
			err = io.ErrUnexpectedEOF
		}
		return Entry{}, 0, err
	}
	p := strings.SplitN(strings.TrimSuffix(s, "\n"), " ", 3)
	if len(p) == 2 && p[0] == "DELETE" {
		return Entry{Key: p[1], Deleted: true}, int64(len(s)), nil
	}
	if len(p) == 3 && p[0] == "PUT" {
		return Entry{Key: p[1], Value: p[2]}, int64(len(s)), nil
	}
	return Entry{}, 0, errors.New("invalid text record")
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
		if count%10 == 0 {
			if _, err = fmt.Fprintf(index, "%s %d\n", e.Key, offset); err != nil {
				index.Close()
				return err
			}
		}
		offset += n
		last = e.Key
		count++
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
	idx, err := os.Open(t.indexPath)
	if err != nil {
		return Entry{}, false, err
	}
	var entries []IndexEntry
	s := bufio.NewScanner(idx)
	for s.Scan() {
		p := strings.SplitN(s.Text(), " ", 2)
		if len(p) != 2 {
			idx.Close()
			return Entry{}, false, errors.New("invalid index")
		}
		offset, err := strconv.ParseInt(p[1], 10, 64)
		if err != nil {
			idx.Close()
			return Entry{}, false, err
		}
		entries = append(entries, IndexEntry{p[0], offset})
	}
	err = s.Err()
	idx.Close()
	if err != nil {
		return Entry{}, false, err
	}
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
