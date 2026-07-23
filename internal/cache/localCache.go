package cache

import (
	"expo-open-ota/internal/version"
	"fmt"
	"sync"
	"time"
)

type LocalCache struct {
	items          map[string]CacheItem
	setItems       map[string]map[string]struct{}
	setExpirations map[string]*time.Time
	mu             sync.RWMutex // RWMutex for safe concurrent access
}

type CacheItem struct {
	Value      string
	Expiration *time.Time // nil if no TTL
}

func NewLocalCache() *LocalCache {
	return &LocalCache{
		items:          make(map[string]CacheItem),
		setItems:       make(map[string]map[string]struct{}),
		setExpirations: make(map[string]*time.Time),
	}
}

func (c *LocalCache) Get(key string) string {
	c.mu.RLock()
	item, exists := c.items[withPrefix(key)]
	c.mu.RUnlock()
	if !exists {
		return ""
	}

	if item.Expiration != nil && time.Now().After(*item.Expiration) {
		// Deleting under the read lock would be a concurrent map write (two
		// Gets racing on an expired key is an unrecoverable runtime fatal):
		// upgrade to the write lock and re-check, a concurrent Set may have
		// refreshed the entry in the gap.
		c.mu.Lock()
		if current, ok := c.items[withPrefix(key)]; ok && current.Expiration != nil && time.Now().After(*current.Expiration) {
			delete(c.items, withPrefix(key))
		}
		c.mu.Unlock()
		return ""
	}

	return item.Value
}

func (c *LocalCache) Set(key string, value string, ttl *int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiration *time.Time
	if ttl != nil {
		exp := time.Now().Add(time.Duration(*ttl) * time.Second)
		expiration = &exp
	}

	c.items[withPrefix(key)] = CacheItem{
		Value:      value,
		Expiration: expiration,
	}
	return nil
}

func (c *LocalCache) Delete(key string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, withPrefix(key))
}

func (c *LocalCache) Clear() error {
	if version.Version != "development" {
		fmt.Println("Cache can only be cleared in development mode.")
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]CacheItem)
	return nil
}

func (c *LocalCache) TryLock(key string, ttl int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.items[withPrefix(key)]; exists {
		return false, nil
	}

	exp := time.Now().Add(time.Duration(ttl) * time.Second)
	c.items[withPrefix(key)] = CacheItem{
		Value:      "locked",
		Expiration: &exp,
	}

	go func() {
		time.Sleep(time.Duration(ttl) * time.Second)
		c.mu.Lock()
		delete(c.items, withPrefix(key))
		c.mu.Unlock()
	}()

	return true, nil
}

func (c *LocalCache) Sadd(key string, members []string, ttl *int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefixedKey := withPrefix(key)

	if _, exists := c.setItems[prefixedKey]; !exists {
		c.setItems[prefixedKey] = make(map[string]struct{})
		if ttl != nil {
			exp := time.Now().Add(time.Duration(*ttl) * time.Second)
			c.setExpirations[prefixedKey] = &exp
		}
	}

	if exp, ok := c.setExpirations[prefixedKey]; ok && time.Now().After(*exp) {
		delete(c.setItems, prefixedKey)
		delete(c.setExpirations, prefixedKey)
		c.setItems[prefixedKey] = make(map[string]struct{})
		if ttl != nil {
			exp := time.Now().Add(time.Duration(*ttl) * time.Second)
			c.setExpirations[prefixedKey] = &exp
		}
	}

	for _, member := range members {
		c.setItems[prefixedKey][member] = struct{}{}
	}
	return nil
}

func (c *LocalCache) Scard(key string) (int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	prefixedKey := withPrefix(key)

	if exp, ok := c.setExpirations[prefixedKey]; ok && time.Now().After(*exp) {
		return 0, nil
	}

	set, exists := c.setItems[prefixedKey]
	if !exists {
		return 0, nil
	}
	return int64(len(set)), nil
}
