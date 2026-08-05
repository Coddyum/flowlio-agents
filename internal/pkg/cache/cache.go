package cache

// Cache is the cache port exposed to the modules through ModuleConfig. The implementation is
// interchangeable (process memory today) without touching the features.
//
// `any` is accepted here: a cache is by nature agnostic of the type it stores. The callers
// type-assert on the way back, on the store side.

import "time"

// Cache is the cache contract consumed by the features.
type Cache interface {
	// Get yields the value associated with key, and whether it was present and unexpired.
	Get(key string) (any, bool)
	// Set writes the value with a TTL. A ttl of 0 uses the implementation's default TTL.
	Set(key string, value any, ttl time.Duration)
	// Delete removes the key, whether it exists or not.
	Delete(key string)
}
