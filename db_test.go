package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTest(t *testing.T, opts Options) *DB {
	t.Helper()
	db, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func want(t *testing.T, db *DB, k, v string, exists bool) {
	t.Helper()
	got, ok, err := db.Get(k)
	if err != nil || ok != exists || (ok && got != v) {
		t.Fatalf("Get(%q)=(%q,%v,%v), want (%q,%v)", k, got, ok, err, v, exists)
	}
}
func TestTombstones(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 2})
	dir := db.dir
	must(t, db.Put("apple", "<DELETED>"))
	must(t, db.Flush())
	want(t, db, "apple", "<DELETED>", true)
	must(t, db.Delete("apple"))
	want(t, db, "apple", "", false)
	must(t, db.Close())
	var err error
	db, err = Open(dir, Options{FlushThreshold: 2})
	must(t, err)
	defer db.Close()
	want(t, db, "apple", "", false)
	must(t, db.Flush())
	want(t, db, "apple", "", false)
	must(t, db.Close())
	db, err = Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "apple", "", false)
	must(t, db.Put("apple", "back"))
	want(t, db, "apple", "back", true)
}
func TestSparseBoundaries(t *testing.T) {
	db := openTest(t, Options{})
	for _, k := range []string{"b", "d", "f", "h", "j", "l", "n", "p", "r", "t", "v", "x"} {
		must(t, db.Put(k, k))
	}
	must(t, db.Flush())
	for _, k := range []string{"b", "t", "v", "x"} {
		want(t, db, k, k, true)
	}
	for _, k := range []string{"a", "c", "u", "z"} {
		want(t, db, k, "", false)
	}
}
func TestRecoveryTailAndLock(t *testing.T) {
	db := openTest(t, Options{})
	dir := db.dir
	must(t, db.Put("key", "value"))
	if second, err := Open(dir, Options{}); err == nil {
		second.Close()
		t.Fatal("opened twice")
	}
	must(t, db.Close())
	f, err := os.OpenFile(filepath.Join(dir, "data.log"), os.O_APPEND|os.O_WRONLY, 0644)
	must(t, err)
	_, err = f.WriteString("PUT broken")
	must(t, err)
	must(t, f.Close())
	db, err = Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "key", "value", true)
	must(t, db.Put("next", "ok"))
	must(t, db.Close())
	db, err = Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "next", "ok", true)
}
func TestLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "sstable_1.log"), []byte("PUT a old\nPUT b keep\n"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "data.log"), []byte("DELETE a\nPUT c new\n"), 0644))
	db, err := Open(dir, Options{})
	must(t, err)
	defer db.Close()
	want(t, db, "a", "", false)
	want(t, db, "b", "keep", true)
	want(t, db, "c", "new", true)
}
func TestClosed(t *testing.T) {
	db := openTest(t, Options{})
	must(t, db.Close())
	if err := db.Put("x", "y"); err != ErrClosed {
		t.Fatal(err)
	}
	if _, _, err := db.Get("x"); err != ErrClosed {
		t.Fatal(err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 73})
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				k := fmt.Sprintf("%d-%d", w, i)
				if err := db.Put(k, k); err != nil {
					t.Error(err)
					return
				}
				v, ok, err := db.Get(k)
				if err != nil || !ok || v != k {
					t.Errorf("read after write: %s %v %v", v, ok, err)
				}
			}
		}(worker)
	}
	wg.Wait()
	must(t, db.Flush())
	for w := 0; w < 16; w++ {
		for i := 0; i < 100; i++ {
			k := fmt.Sprintf("%d-%d", w, i)
			want(t, db, k, k, true)
		}
	}
}

func TestBinaryRoundTrip(t *testing.T) {
	db := openTest(t, Options{})
	keys := []string{"", "a b\nc\x00", "日本語", "PUT"}
	values := []string{"", " \n\t\x00\xff", strings.Repeat("v", 1<<20), "<DELETED>"}
	for i, k := range keys {
		must(t, db.Put(k, values[i]))
	}
	must(t, db.Flush())
	for i, k := range keys {
		want(t, db, k, values[i], true)
	}
	dir := db.dir
	must(t, db.Close())
	db, err := Open(dir, Options{})
	must(t, err)
	defer db.Close()
	for i, k := range keys {
		want(t, db, k, values[i], true)
	}
}
func TestBinaryCorruption(t *testing.T) {
	var b bytes.Buffer
	_, err := writeRecord(&b, Entry{Key: "key", Value: "value"})
	must(t, err)
	encoded := append([]byte(nil), b.Bytes()...)
	for cut := 1; cut < len(encoded); cut++ {
		_, _, err := readRecord(bufio.NewReader(bytes.NewReader(encoded[:cut])))
		if err != io.ErrUnexpectedEOF {
			t.Fatalf("cut %d: %v", cut, err)
		}
	}
	encoded[len(encoded)-1] ^= 1
	if _, _, err := readRecord(bufio.NewReader(bytes.NewReader(encoded))); err == nil {
		t.Fatal("corruption accepted")
	}
}

func TestIndexResident(t *testing.T) {
	db := openTest(t, Options{})
	must(t, db.Put("key", "value"))
	must(t, db.Flush())
	must(t, os.Remove(db.tables[0].indexPath))
	want(t, db, "key", "value", true)
}

func TestMappedReads(t *testing.T) {
	db := openTest(t, Options{})
	must(t, db.Put("a", "x"))
	must(t, db.Flush())
	table := db.tables[0]
	if len(table.data) == 0 || len(table.indexData) == 0 {
		t.Fatal("files not mapped")
	}
	must(t, os.Rename(table.path, table.path+".moved"))
	want(t, db, "a", "x", true)
	must(t, db.Close())
	if table.data != nil || table.indexData != nil {
		t.Fatal("mapping not released")
	}
}

func TestPointReadChecksSkippedRecords(t *testing.T) {
	db := openTest(t, Options{CacheBytes: -1, FlushThreshold: 10000})
	for i := 0; i < 10; i++ {
		must(t, db.Put(fmt.Sprintf("key%d", i), "payload"))
	}
	must(t, db.Flush())
	table := db.tables[0]
	original := table.data
	table.data = append([]byte(nil), original...)
	defer func() { table.data = original }()
	var offset int64
	for i := 0; i < 4; i++ {
		_, n, err := recordViewAt(table.data, offset)
		must(t, err)
		offset += n
	}
	table.data[offset+recordHeader] ^= 1
	if _, _, err := db.Get("key9"); err == nil {
		t.Fatal("lookup ignored corruption in a traversed record")
	}
}

func TestPointReadOwnsReturnedValue(t *testing.T) {
	db := openTest(t, Options{CacheBytes: -1})
	value := strings.Repeat("owned", 100)
	must(t, db.Put("key", value))
	must(t, db.Flush())
	got, ok, err := db.Get("key")
	must(t, err)
	must(t, db.Close())
	if !ok || got != value {
		t.Fatal("returned value did not survive unmapping")
	}
}

func TestStreamingTableMetadataMatchesRebuiltIndex(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: -1, FlushThreshold: 10000})
	for i := 0; i < 113; i++ {
		key := fmt.Sprintf("key%04d", i)
		must(t, db.Put(key, strings.Repeat("value", i%9)))
	}
	must(t, db.Delete("key0020"))
	must(t, db.Flush())
	table := db.tables[0]
	originalIndex, err := os.ReadFile(table.indexPath)
	must(t, err)
	count, minKey, maxKey := table.count, table.minKey, table.maxKey
	must(t, table.rebuildIndex())
	rebuilt, err := os.ReadFile(table.indexPath)
	must(t, err)
	if !bytes.Equal(originalIndex, rebuilt) || table.count != count || table.minKey != minKey || table.maxKey != maxKey {
		t.Fatal("streaming metadata differs from validated rebuild")
	}
	for i := 0; i < 113; i++ {
		key := fmt.Sprintf("key%04d", i)
		value := strings.Repeat("value", i%9)
		if i == 20 {
			value = ""
		}
		want(t, db, key, value, i != 20)
	}
}

func TestStreamingTableRejectsInvalidIterator(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: -1})
	i := 0
	_, err := db.writeTableIterator(func() (Entry, bool, error) {
		i++
		switch i {
		case 1:
			return Entry{Key: "z", Value: "one"}, true, nil
		case 2:
			return Entry{Key: "a", Value: "two"}, true, nil
		default:
			return Entry{}, false, nil
		}
	})
	if err == nil {
		t.Fatal("unsorted stream accepted")
	}
	must(t, db.Put("safe", "value"))
	must(t, db.Flush())
	want(t, db, "safe", "value", true)
}
