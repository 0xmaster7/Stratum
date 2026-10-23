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
	h := &mergeHeap{}
	for _, t := range tables {
		offset := int64(0)
		if start != "" {
			i := indexBefore(t.index, start)
			if i >= 0 {
				offset = t.index[i].Offset
			}
		}
		e, n, err := recordAt(t.data, offset)
		if err == io.EOF {
			continue
		}
		if err != nil {
			return nil, err
		}
		heap.Push(h, &tableCursor{table: t, offset: offset + n, entry: e})
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
func (db *DB) Compact() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	if db.writeErr != nil {
		return db.writeErr
	}
	if err := db.flushLocked(); err != nil {
		return err
	}
	if len(db.tables) < 2 {
		return nil
	}
	started := time.Now()
	old := append([]*sstable(nil), db.tables...)
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
	if err = db.install([]*sstable{t}); err != nil {
		t.close()
		return err
	}
	db.cache.clear()
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
	if db.opts.CompactionInterval < 0 {
		<-db.stop
		return
	}
	ticker := time.NewTicker(db.opts.CompactionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-db.stop:
			return
		case <-ticker.C:
			db.mu.RLock()
			needed := !db.closed && len(db.tables) >= db.opts.CompactionThreshold
			db.mu.RUnlock()
			if needed {
				if err := db.Compact(); err != nil && err != ErrClosed {
					db.mu.Lock()
					db.backgroundErr = err
					db.mu.Unlock()
				}
			}
		}
	}
}
