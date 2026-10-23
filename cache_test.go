package main

import "testing"

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
