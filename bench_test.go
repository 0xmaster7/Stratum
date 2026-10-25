package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const benchKeys = 10000

var benchValue = strings.Repeat("v", 100)

func benchmarkKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%012d", i*2)
	}
	return keys
}
func benchmarkDB(b *testing.B, opts Options, seed bool) *DB {
	b.Helper()
	opts.CompactionInterval = -1
	db, err := Open(b.TempDir(), opts)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Error(err)
		}
	})
	if seed {
		for _, k := range benchmarkKeys(benchKeys) {
			if err := db.Put(k, benchValue); err != nil {
				b.Fatal(err)
			}
		}
	}
	return db
}
func BenchmarkPut(b *testing.B) {
	for _, syncWrites := range []bool{false, true} {
		name := "Buffered"
		if syncWrites {
			name = "Sync"
		}
		b.Run(name, func(b *testing.B) {
			db := benchmarkDB(b, Options{SyncWrites: syncWrites}, false)
			keys := benchmarkKeys(benchKeys)
			b.ReportAllocs()
			b.SetBytes(116)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.Put(keys[i%len(keys)], benchValue); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func BenchmarkGet(b *testing.B) {
	for _, mode := range []string{"Memtable", "SSTable", "Cached", "MissingBloom", "MissingNoBloom"} {
		b.Run(mode, func(b *testing.B) {
			opts := Options{FlushThreshold: 1 << 30, CacheBytes: -1}
			if mode == "Cached" {
				opts.CacheBytes = 16 << 20
			}
			db := benchmarkDB(b, opts, true)
			keys := benchmarkKeys(benchKeys)
			if mode != "Memtable" {
				if err := db.Flush(); err != nil {
					b.Fatal(err)
				}
			}
			if mode == "Cached" {
				for _, k := range keys {
					if _, ok, err := db.Get(k); err != nil || !ok {
						b.Fatal(k, err)
					}
				}
			}
			missing := strings.HasPrefix(mode, "Missing")
			if missing {
				for i := range keys {
					keys[i] = fmt.Sprintf("key-%012d", i*2+1)
				}
			}
			if mode == "MissingNoBloom" {
				for _, t := range db.tables {
					t.bloom = nil
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, ok, err := db.Get(keys[i%len(keys)])
				if err != nil || ok == missing {
					b.Fatal(keys[i%len(keys)], ok, err)
				}
			}
		})
	}
}
func BenchmarkGetParallel(b *testing.B) {
	db := benchmarkDB(b, Options{FlushThreshold: 1 << 30}, true)
	keys := benchmarkKeys(benchKeys)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, ok, err := db.Get(keys[i%len(keys)])
			if err != nil || !ok {
				b.Error(err)
			}
			i++
		}
	})
}
func BenchmarkMixed(b *testing.B) {
	for _, reads := range []int{50, 95} {
		b.Run(fmt.Sprintf("Read%dWrite%d", reads, 100-reads), func(b *testing.B) {
			db := benchmarkDB(b, Options{}, true)
			keys := benchmarkKeys(benchKeys)
			var workers atomic.Uint64
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				r := workers.Add(1)
				for pb.Next() {
					r ^= r << 13
					r ^= r >> 7
					r ^= r << 17
					k := keys[int(r%uint64(len(keys)))]
					if int((r>>32)%100) < reads {
						if _, ok, err := db.Get(k); err != nil || !ok {
							b.Error(err)
						}
					} else {
						if err := db.Put(k, benchValue); err != nil {
							b.Error(err)
						}
					}
				}
			})
		})
	}
}
func BenchmarkScan100(b *testing.B) {
	db := benchmarkDB(b, Options{}, true)
	if err := db.Flush(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Scan("key-000000010000", "", 100)
		if err != nil || len(rows) != 100 {
			b.Fatal(len(rows), err)
		}
	}
}
func BenchmarkFlush1000(b *testing.B) {
	db := benchmarkDB(b, Options{FlushThreshold: 1 << 30}, false)
	keys := benchmarkKeys(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for _, k := range keys {
			if err := db.Put(k, benchValue); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if err := db.Flush(); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkCompaction(b *testing.B) {
	root := b.TempDir()
	keys := benchmarkKeys(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := filepath.Join(root, fmt.Sprint(i))
		db, err := Open(dir, Options{CompactionInterval: -1, FlushThreshold: 1 << 30})
		if err != nil {
			b.Fatal(err)
		}
		for round := 0; round < 4; round++ {
			for _, k := range keys {
				if err = db.Put(k, benchValue); err != nil {
					b.Fatal(err)
				}
			}
			if err = db.Flush(); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		err = db.Compact()
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if err = db.Close(); err != nil {
			b.Fatal(err)
		}
		if err = os.RemoveAll(dir); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
func BenchmarkRecovery20000(b *testing.B) {
	dir := b.TempDir()
	opts := Options{CompactionInterval: -1, FlushThreshold: 1 << 30}
	db, err := Open(dir, opts)
	if err != nil {
		b.Fatal(err)
	}
	for _, k := range benchmarkKeys(20000) {
		if err = db.Put(k, benchValue); err != nil {
			b.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db, err = Open(dir, opts)
		if err != nil {
			b.Fatal(err)
		}
		if db.operations.Load() != 20000 {
			b.Fatal("recovery count")
		}
		if err = db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// Repeated small ranges over a mixed memory/disk layout, as in latest-for-agent.
func BenchmarkScanLatest10(b *testing.B) {
	for _, pending := range []int{0, 900} {
		b.Run(fmt.Sprintf("Pending%d", pending), func(b *testing.B) {
			db := benchmarkDB(b, Options{FlushThreshold: 1 << 30}, true)
			if err := db.Flush(); err != nil {
				b.Fatal(err)
			}
			keys := benchmarkKeys(benchKeys)
			for i := 0; i < pending; i++ {
				if err := db.Put(keys[i], "updated"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows, err := db.Scan(keys[5000], keys[5020], 10)
				if err != nil || len(rows) != 10 {
					b.Fatal(len(rows), err)
				}
			}
		})
	}
}

// Diagnostic benchmark: normal background maintenance, including final drain.
// CPU/block/syscall profiles cover the entire test process, not only its timer.
func BenchmarkMaintenanceWritePath(b *testing.B) {
	db, err := Open(b.TempDir(), Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Error(err)
		}
	})
	keys := benchmarkKeys(benchKeys)
	value := strings.Repeat("v", 291)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Put(keys[i%len(keys)], value); err != nil {
			b.Fatal(err)
		}
	}
	if err := db.Flush(); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	metrics := db.Stats()
	if metrics.WriteError != "" || metrics.BackgroundError != "" {
		b.Fatal(metrics)
	}
	b.ReportMetric(float64(metrics.Flushes), "flushes")
	b.ReportMetric(float64(metrics.Compactions), "compactions")
}
