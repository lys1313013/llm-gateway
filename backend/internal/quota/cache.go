package quota

import "sync"

// Cache is an in-memory store of the latest snapshot per provider_id.
// Safe for concurrent use.
type Cache struct {
	mu        sync.RWMutex
	snapshots map[int]Snapshot
}

func NewCache() *Cache {
	return &Cache{snapshots: make(map[int]Snapshot)}
}

func (c *Cache) Set(providerID int, snap Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshots[providerID] = snap
}

// Get returns the snapshot and whether it exists. A provider that has never
// been refreshed returns (zero, false).
func (c *Cache) Get(providerID int) (Snapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.snapshots[providerID]
	return s, ok
}

// GetAll returns a copy of every cached snapshot keyed by provider id.
func (c *Cache) GetAll() map[int]Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int]Snapshot, len(c.snapshots))
	for k, v := range c.snapshots {
		out[k] = v
	}
	return out
}
