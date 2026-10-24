package main

import (
	"container/heap"
	"errors"
	"io"
	"os"
	"time"
)

type tableCursor struct {
	table  *sstable
	offset int64
	entry  Entry
}
type mergeHeap []*tableCursor

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].entry.Key == h[j].entry.Key {
		return h[i].table.id > h[j].table.id
	}
	return h[i].entry.Key < h[j].entry.Key
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)   { *h = append(*h, x.(*tableCursor)) }
func (h *mergeHeap) Pop() any     { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }
func mergedIterator(tables []*sstable, start string) (func() (Entry, bool, error), error) {
	return mergedRangeIterator(tables, start, "")
}
func mergedRangeIterator(tables []*sstable, start, end string) (func() (Entry, bool, error), error) {
	h := &mergeHeap{}
	for _, t := range tables {
		if t.count == 0 || t.maxKey < start || (end != "" && t.minKey >= end) {
			continue
		}
		offset := int64(0)
		if start != "" {
			i := indexBefore(t.index, start)
			if i >= 0 {
				offset = t.index[i].Offset
			}
		}
		e, n, err := recordAt(t.data, offset)
		for err == nil && e.Key < start {
			offset += n
			e, n, err = recordAt(t.data, offset)
		}
		if err == io.EOF {
			continue
		}
		if err != nil {
			return nil, err
		}
		if end == "" || e.Key < end {
			heap.Push(h, &tableCursor{table: t, offset: offset + n, entry: e})
		}
	}
	var previous string
	seen := false
	return func() (Entry, bool, error) {
		for h.Len() > 0 {
			cursor := heap.Pop(h).(*tableCursor)
			e := cursor.entry
			next, n, err := recordAt(cursor.table.data, cursor.offset)
			if err != nil && err != io.EOF {
				return Entry{}, false, err
			}
			if err == nil {
				cursor.entry = next
				cursor.offset += n
				heap.Push(h, cursor)
			}
			if seen && e.Key == previous {
				continue
			}
			previous = e.Key
			seen = true
			return e, true, nil
		}
		return Entry{}, false, nil
	}, nil
}
func (db *DB) Compact() error { return db.compact(nil) }

// afterSnapshot allows deterministic scheduling in concurrency tests.
func (db *DB) compact(afterSnapshot func()) error {
	if err := db.Flush(); err != nil {
		return err
	}
	db.maintenanceMu.Lock()
	defer db.maintenanceMu.Unlock()
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrClosed
	}
	if db.writeErr != nil {
		err := db.writeErr
		db.mu.Unlock()
		return err
	}
	if len(db.tables) < 2 {
		db.mu.Unlock()
		return nil
	}
	started := time.Now()
	old := append([]*sstable(nil), db.tables...)
	db.mu.Unlock()
	// Maintenance serialization pins the input mappings. Ordinary readers and
	// writers may use the existing view throughout construction.
	if afterSnapshot != nil {
		afterSnapshot()
	}
	next, err := mergedIterator(old, "")
	if err != nil {
		return err
	}
	live := func() (Entry, bool, error) {
		for {
			e, ok, err := next()
			if err != nil || !ok {
				return e, ok, err
			}
			if !e.Deleted {
				return e, true, nil
			}
		}
	}
	t, err := db.writeTableIterator(live)
	if err != nil {
		return err
	}
	db.mu.Lock()
	if err = db.install([]*sstable{t}); err != nil {
		db.mu.Unlock()
		t.close()
		return err
	}
	// Acquiring the exclusive lock has drained every reader of the old view.
	db.mu.Unlock()
	var cleanupErr error
	for _, oldTable := range old {
		cleanupErr = errors.Join(cleanupErr, oldTable.close())
		for _, path := range []string{oldTable.path, oldTable.indexPath} {
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				cleanupErr = errors.Join(cleanupErr, e)
			}
		}
	}
	cleanupErr = errors.Join(cleanupErr, syncDir(db.dir))
	if cleanupErr != nil {
		return cleanupErr
	}
	db.metrics.compactions.Add(1)
	db.metrics.compactionNanos.Add(uint64(time.Since(started)))
	return nil
}
func (db *DB) runCompactor() {
	defer close(db.done)
	var ticks <-chan time.Time
	if db.opts.CompactionInterval > 0 {
		ticker := time.NewTicker(db.opts.CompactionInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		var err error
		select {
		case <-db.stop:
			return
		case <-db.flushWake:
			err = db.flushPending()
		case <-ticks:
			db.mu.RLock()
			needed := !db.closed && len(db.tables) >= db.opts.CompactionThreshold
			db.mu.RUnlock()
			if needed {
				err = db.Compact()
			}
		}
		if err != nil && err != ErrClosed {
			db.mu.Lock()
			db.backgroundErr = err
			db.mu.Unlock()
		}
	}
}
