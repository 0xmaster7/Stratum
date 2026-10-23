package main

import (
	"container/list"
	"sync"
)

type cachedValue struct {
	key, value string
	bytes      int
}
type readCache struct {
	mu           sync.Mutex
	budget, used int
	items        map[string]*list.Element
	lru          *list.List
}

func newCache(budget int) *readCache {
	return &readCache{budget: budget, items: make(map[string]*list.Element), lru: list.New()}
}
func (c *readCache) get(key string) (string, bool) {
	if c.budget <= 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.lru.MoveToFront(e)
	return e.Value.(cachedValue).value, true
}
func (c *readCache) removeLocked(key string) {
	if e, ok := c.items[key]; ok {
		c.used -= e.Value.(cachedValue).bytes
		c.lru.Remove(e)
		delete(c.items, key)
	}
}
func (c *readCache) remove(key string) {
	if c.budget <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}
func (c *readCache) put(key, value string) {
	if c.budget <= 0 {
		return
	}
	size := len(key) + len(value) + 64
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
	if size > c.budget {
		return
	}
	for c.used+size > c.budget {
		c.removeLocked(c.lru.Back().Value.(cachedValue).key)
	}
	c.items[key] = c.lru.PushFront(cachedValue{key, value, size})
	c.used += size
}
func (c *readCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.used = 0
}
