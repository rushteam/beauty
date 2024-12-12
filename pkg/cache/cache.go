package cache

import (
	"sync"
	"time"
)

type Cache struct {
	mu      sync.RWMutex
	items   map[string]*Item
	maxSize int
	onEvict func(key string, value interface{})
}

type Item struct {
	Value      interface{}
	Expiration int64
}

func NewCache(maxSize int) *Cache {
	c := &Cache{
		items:   make(map[string]*Item),
		maxSize: maxSize,
	}
	go c.cleanupLoop()
	return c
}

func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxSize {
		c.evict()
	}

	c.items[key] = &Item{
		Value:      value,
		Expiration: time.Now().Add(ttl).UnixNano(),
	}
}

func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}

	if item.Expiration > 0 && time.Now().UnixNano() > item.Expiration {
		return nil, false
	}

	return item.Value, true
}

func (c *Cache) evict() {
	var oldestKey string
	var oldestExpiration int64 = time.Now().UnixNano()

	for key, item := range c.items {
		if item.Expiration < oldestExpiration {
			oldestKey = key
			oldestExpiration = item.Expiration
		}
	}

	if c.onEvict != nil {
		c.onEvict(oldestKey, c.items[oldestKey].Value)
	}
	delete(c.items, oldestKey)
}

func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now().UnixNano()
		for key, item := range c.items {
			if item.Expiration > 0 && now > item.Expiration {
				if c.onEvict != nil {
					c.onEvict(key, item.Value)
				}
				delete(c.items, key)
			}
		}
		c.mu.Unlock()
	}
}
