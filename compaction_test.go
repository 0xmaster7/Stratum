package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCompaction(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: -1})
	dir := db.dir
	for round := 0; round < 4; round++ {
		must(t, db.Put("a", fmt.Sprint(round)))
		must(t, db.Put("gone", "old"))
		must(t, db.Put("", "empty"))
		must(t, db.Flush())
	}
	must(t, db.Delete("gone"))
	must(t, db.Delete(""))
	must(t, db.Compact())
	if len(db.tables) != 1 || db.tables[0].count != 1 {
		t.Fatalf("compaction retained obsolete entries: %+v", db.tables)
	}
	want(t, db, "a", "3", true)
	want(t, db, "gone", "", false)
	want(t, db, "", "", false)
	files, err := filepath.Glob(filepath.Join(dir, "sstable_*.log"))
	must(t, err)
	if len(files) != 1 {
		t.Fatal(files)
	}
	must(t, db.Close())
	db, err = Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "gone", "", false)
	want(t, db, "a", "3", true)
	must(t, db.Delete("a"))
	must(t, db.Compact())
	want(t, db, "a", "", false)
	if db.tables[0].count != 0 {
		t.Fatal("empty compaction output")
	}
}
func TestManifestIgnoresUnpublishedTables(t *testing.T) {
	db := openTest(t, Options{})
	dir := db.dir
	must(t, db.Put("a", "good"))
	must(t, db.Flush())
	must(t, db.Close())
	must(t, os.WriteFile(filepath.Join(dir, "sstable_999.log"), []byte("PUT a wrong\n"), 0644))
	db, err := Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "a", "good", true)
	must(t, db.Put("b", "next"))
	must(t, db.Flush())
	if db.tables[len(db.tables)-1].id <= 999 {
		t.Fatal("reused orphan ID")
	}
}
func TestMissingManifestTable(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(`{"tables":[7]}`), 0644))
	if db, err := Open(dir, Options{}); err == nil {
		db.Close()
		t.Fatal("missing table ignored")
	}
}
func TestWALReplayAfterPublishedFlush(t *testing.T) {
	db := openTest(t, Options{})
	dir := db.dir
	must(t, db.Put("key", "old"))
	must(t, db.Flush())
	must(t, db.Put("key", "new"))
	wal, err := os.ReadFile(filepath.Join(dir, "data.log"))
	must(t, err)
	must(t, db.Flush())
	must(t, db.Close())
	must(t, os.WriteFile(filepath.Join(dir, "data.log"), wal, 0644))
	db, err = Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "key", "new", true)
	must(t, db.Compact())
	want(t, db, "key", "new", true)
}
func TestBackgroundCompaction(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: time.Millisecond, CompactionThreshold: 2})
	must(t, db.Put("a", "old"))
	must(t, db.Flush())
	must(t, db.Put("a", "new"))
	must(t, db.Flush())
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		db.mu.RLock()
		n := len(db.tables)
		db.mu.RUnlock()
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker did not compact")
		case <-ticker.C:
		}
	}
	want(t, db, "a", "new", true)
}
func TestRandomizedModel(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 37, CompactionInterval: -1})
	dir := db.dir
	model := map[string]string{}
	r := rand.New(rand.NewSource(2026))
	for step := 0; step < 600; step++ {
		k := fmt.Sprintf("key%02d", r.Intn(60))
		switch r.Intn(8) {
		case 0, 1:
			must(t, db.Delete(k))
			delete(model, k)
		case 2:
			must(t, db.Flush())
		case 3:
			must(t, db.Compact())
		case 4:
			must(t, db.Close())
			var err error
			db, err = Open(dir, Options{FlushThreshold: 37, CompactionInterval: -1})
			must(t, err)
		default:
			v := fmt.Sprint(step)
			must(t, db.Put(k, v))
			model[k] = v
		}
		for i := 0; i < 60; i++ {
			k := fmt.Sprintf("key%02d", i)
			v, ok := model[k]
			want(t, db, k, v, ok)
		}
	}
	must(t, db.Close())
}

func TestManifestFailureKeepsWAL(t *testing.T) {
	db := openTest(t, Options{})
	dir := db.dir
	must(t, db.Put("key", "safe"))
	must(t, os.Mkdir(filepath.Join(dir, "MANIFEST.tmp"), 0755))
	if err := db.Flush(); err == nil {
		t.Fatal("publication unexpectedly succeeded")
	}
	if err := db.Put("next", "rejected"); err == nil {
		t.Fatal("writes continued after publication failure")
	}
	if db.Stats().WriteError == "" {
		t.Fatal("write error not exposed")
	}
	want(t, db, "key", "safe", true)
	must(t, db.Close())
	must(t, os.Remove(filepath.Join(dir, "MANIFEST.tmp")))
	db, err := Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "key", "safe", true)
	want(t, db, "next", "", false)
	must(t, db.Flush())
	want(t, db, "key", "safe", true)
}
func TestConcurrentCompactionAndStats(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 41, CompactionInterval: time.Millisecond, CompactionThreshold: 2})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 80; i++ {
				k := fmt.Sprintf("%d-%d", w, i)
				if err := db.Put(k, k); err != nil {
					t.Error(err)
					return
				}
				if i%20 == 0 {
					db.Stats()
					if _, err := db.Scan("", "", 3); err != nil {
						t.Error(err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	must(t, db.Compact())
	for w := 0; w < 4; w++ {
		for i := 0; i < 80; i++ {
			k := fmt.Sprintf("%d-%d", w, i)
			want(t, db, k, k, true)
		}
	}
}
