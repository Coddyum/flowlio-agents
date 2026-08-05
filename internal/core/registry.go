package core

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                     | Ligne |
// |-------------------|------------------------------------------------------------|-------|
// | registry          | Concurrency-safe implementation of the FeatureRegistry       | 24    |
// | NewRegistry       | Creates an empty registry, ready to receive the modules      | 30    |
// | registry.Register | Registers a provider under its module key                    | 35    |
// | registry.Get      | Resolves a provider by key, without importing the feature    | 43    |
//
// Fin du sommaire.
// =====================================================================

import (
	"sync"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
)

// registry is the inter-feature meeting point: every module registers itself there at
// start-up, the others resolve it lazily. Protected by a mutex because the resolutions happen
// while requests are being served.
type registry struct {
	mu        sync.RWMutex
	providers map[string]any
}

// NewRegistry creates an empty registry. Filling it happens in cmd/api/main.go.
func NewRegistry() module.FeatureRegistry {
	return &registry{providers: make(map[string]any)}
}

// Register associates a provider with a module key. A re-registration overwrites the previous one.
func (r *registry) Register(key string, provider any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[key] = provider
}

// Get yields the provider registered under key. It is up to the consumer to type-assert on the
// interface it declares on its own side.
func (r *registry) Get(key string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[key]
	return p, ok
}
