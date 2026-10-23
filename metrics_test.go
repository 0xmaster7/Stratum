package main

import "testing"

func TestMetrics(t *testing.T) {
	db := openTest(t, Options{})
	must(t, db.Put("a", "value"))
	must(t, db.Put("z", "last"))
	want(t, db, "a", "value", true)
	must(t, db.Flush())
	want(t, db, "a", "value", true)
	want(t, db, "a", "value", true)
	want(t, db, "missing", "", false)
	must(t, db.Delete("a"))
	must(t, db.Compact())
	_, err := db.Scan("", "", 0)
	must(t, err)
	m := db.Stats()
	if m.Puts != 2 || m.Deletes != 1 || m.Gets != 4 || m.Misses != 1 || m.MemtableHits != 1 || m.CacheHits != 1 || m.Flushes != 2 || m.Compactions != 1 || m.Scans != 1 || m.SSTables != 1 || m.MappedBytes == 0 || m.BloomBytes == 0 || m.DiskBytes == 0 || m.WALBytesWritten == 0 || m.LogicalReadBytes == 0 {
		t.Fatalf("metrics: %+v", m)
	}
}
