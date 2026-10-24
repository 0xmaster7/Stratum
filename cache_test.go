package main

import (
	"fmt"
	"testing"
)

func TestCacheBudget(t *testing.T) {
	c := newCache(140)
	c.put("a", "value")
	c.put("b", "value")
	if _, ok := c.get("a"); !ok {
		t.Fatal("missing a")
	}
	c.put("c", "value")
	if _, ok := c.get("b"); ok {
		t.Fatal("least recently used entry retained")
	}
	c.put("huge", string(make([]byte, 200)))
	if c.used > c.budget {
		t.Fatal("budget exceeded")
	}
	c.clear()
	if c.used != 0 || c.lru.Len() != 0 {
		t.Fatal("clear")
	}
}
func TestCacheInvalidation(t *testing.T) {
	db := openTest(t, Options{CacheBytes: 1024})
	must(t, db.Put("a", "old"))
	must(t, db.Flush())
	want(t, db, "a", "old", true)
	if _, ok := db.cache.get("a"); !ok {
		t.Fatal("not cached")
	}
	must(t, db.Put("a", "new"))
	want(t, db, "a", "new", true)
	must(t, db.Flush())
	want(t, db, "a", "new", true)
	must(t, db.Delete("a"))
	must(t, db.Flush())
	want(t, db, "a", "", false)
}

func TestCacheRetainedAcrossMaintenance(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: -1})
	must(t, db.Put("hot", "original"))
	must(t, db.Flush())
	want(t, db, "hot", "original", true)
	must(t, db.Put("other", "value"))
	must(t, db.Flush())
	if value, ok := db.cache.get("hot"); !ok || value != "original" {
		t.Fatal("flush discarded valid cache entry")
	}
	must(t, db.Compact())
	if value, ok := db.cache.get("hot"); !ok || value != "original" {
		t.Fatal("compaction discarded valid cache entry")
	}
	must(t, db.Put("hot", "updated"))
	must(t, db.Flush())
	want(t, db, "hot", "updated", true)
	must(t, db.Delete("hot"))
	must(t, db.Compact())
	want(t, db, "hot", "", false)
}

func TestCacheConcurrentMutationAndFlush(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 10000, CompactionInterval: -1})
	must(t, db.Put("key", "initial"))
	must(t, db.Flush())
	for i := 0; i < 30; i++ {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 50; j++ {
				if _, _, err := db.Get("key"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		value := fmt.Sprint(i)
		must(t, db.Put("key", value))
		must(t, db.Flush())
		<-done
		want(t, db, "key", value, true)
	}
}
