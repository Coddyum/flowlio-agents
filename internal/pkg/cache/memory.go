package cache

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                        | Ligne |
// |---------------|---------------------------------------------------------------|-------|
// | memory        | Cache en mémoire process, adossé à go-cache                     | 24    |
// | NewMemory     | Crée le cache mémoire avec TTL par défaut et purge périodique   | 30    |
// | memory.Get    | Lit une clé si elle est présente et non expirée                 | 35    |
// | memory.Set    | Écrit une clé avec son TTL (0 = TTL par défaut)                 | 40    |
// | memory.Delete | Supprime une clé                                                | 48    |
//
// Fin du sommaire.
// =====================================================================

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// memory est un cache local au process : pas de partage entre instances. Suffisant tant que
// l'API tourne en instance unique ; à remplacer par une implémentation distribuée sinon.
type memory struct {
	c *gocache.Cache
}

// NewMemory crée le cache mémoire. defaultTTL s'applique aux Set sans TTL explicite,
// cleanupInterval règle la fréquence de purge des entrées expirées.
func NewMemory(defaultTTL, cleanupInterval time.Duration) Cache {
	return &memory{c: gocache.New(defaultTTL, cleanupInterval)}
}

// Get lit une clé si elle est présente et non expirée.
func (m *memory) Get(key string) (any, bool) {
	return m.c.Get(key)
}

// Set écrit la valeur avec son TTL ; un ttl de 0 retombe sur le TTL par défaut du cache.
func (m *memory) Set(key string, value any, ttl time.Duration) {
	if ttl == 0 {
		ttl = gocache.DefaultExpiration
	}
	m.c.Set(key, value, ttl)
}

// Delete supprime la clé, qu'elle existe ou non.
func (m *memory) Delete(key string) {
	m.c.Delete(key)
}
