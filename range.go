package main

import (
	"errors"
	"sort"
	"time"
)

// Scan returns live keys in the half-open interval [start,end), in bytewise order.
// An empty end is unbounded; a zero limit returns the entire range. The exclusive
// database lock gives each scan a consistent view while merging disk and memory.
func (db *DB) Scan(start, end string, limit int) ([]Entry, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, ErrClosed
	}
	if limit < 0 {
		return nil, errors.New("scan limit must be nonnegative")
	}
	if end != "" && start > end {
		return nil, errors.New("scan end precedes start")
	}
	started := time.Now()
	defer func() { db.metrics.scans.Add(1); db.metrics.scanNanos.Add(uint64(time.Since(started))) }()
	mem := db.mem.sortedEntries()
	i := sort.Search(len(mem), func(i int) bool { return mem[i].Key >= start })
	next, err := mergedRangeIterator(db.tables, start, end)
	if err != nil {
		return nil, err
	}
	disk, hasDisk, err := next()
	if err != nil {
		return nil, err
	}
	result := make([]Entry, 0)
	for i < len(mem) || hasDisk {
		var e Entry
		advanceDisk := false
		if i < len(mem) && (!hasDisk || mem[i].Key <= disk.Key) {
			e = mem[i]
			i++
			advanceDisk = hasDisk && e.Key == disk.Key
		} else {
			e = disk
			advanceDisk = true
		}
		if end != "" && e.Key >= end {
			break
		}
		if e.Key >= start && !e.Deleted {
			result = append(result, e)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
		if advanceDisk {
			disk, hasDisk, err = next()
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
