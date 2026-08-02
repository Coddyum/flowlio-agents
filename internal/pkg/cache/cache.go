package cache

// Cache est le port de cache exposé aux modules via ModuleConfig. L'implémentation est
// interchangeable (mémoire process aujourd'hui) sans toucher aux features.
//
// `any` est assumé ici : un cache est par nature agnostique du type stocké. Les appelants
// type-assert au retour, côté store.

import "time"

// Cache est le contrat de cache consommé par les features.
type Cache interface {
	// Get renvoie la valeur associée à key et si elle était présente et non expirée.
	Get(key string) (any, bool)
	// Set écrit la valeur avec un TTL. Un ttl de 0 utilise le TTL par défaut de l'implémentation.
	Set(key string, value any, ttl time.Duration)
	// Delete supprime la clé, qu'elle existe ou non.
	Delete(key string)
}
