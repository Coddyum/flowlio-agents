package cache

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                        | Ligne |
// |---------------|---------------------------------------------------------------|-------|
// | memory        | Process-memory cache, backed by go-cache                        | 24    |
// | NewMemory     | Creates the memory cache with a default TTL and periodic purge  | 30    |
// | memory.Get    | Reads a key if it is present and unexpired                      | 35    |
// | memory.Set    | Writes a key with its TTL (0 = default TTL)                     | 40    |
// | memory.Delete | Removes a key                                                   | 48    |
//
// Fin du sommaire.
// =====================================================================

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// memory is a process-local cache: no sharing between instances. Enough for as long as
// the API runs as a single instance; to be replaced by a distributed implementation otherwise.
type memory struct {
	c *gocache.Cache
}

// NewMemory creates the memory cache. defaultTTL applies to Set calls without an explicit TTL,
// cleanupInterval sets how often expired entries are purged.
func NewMemory(defaultTTL, cleanupInterval time.Duration) Cache {
	return &memory{c: gocache.New(defaultTTL, cleanupInterval)}
}

// Get reads a key if it is present and unexpired.
func (m *memory) Get(key string) (any, bool) {
	return m.c.Get(key)
}

// Set writes the value with its TTL; a ttl of 0 falls back on the cache's default TTL.
func (m *memory) Set(key string, value any, ttl time.Duration) {
	if ttl == 0 {
		ttl = gocache.DefaultExpiration
	}
	m.c.Set(key, value, ttl)
}

// Delete removes the key, whether it exists or not.
func (m *memory) Delete(key string) {
	m.c.Delete(key)
}
