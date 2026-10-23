package main

import (
	"os"
	"path/filepath"
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
