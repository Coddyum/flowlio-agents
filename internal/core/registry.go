package core

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                     | Ligne |
// |-------------------|------------------------------------------------------------|-------|
// | registry          | Implémentation concurrente du FeatureRegistry                | 24    |
// | NewRegistry       | Crée un registry vide, prêt à recevoir les modules           | 30    |
// | registry.Register | Enregistre un fournisseur sous sa clé de module              | 35    |
// | registry.Get      | Résout un fournisseur par clé, sans import de la feature     | 43    |
//
// Fin du sommaire.
// =====================================================================

import (
	"sync"

	"github.com/Coddyum/flowlio-ia/internal/core/module"
)

// registry est le point de rendez-vous inter-features : chaque module s'y enregistre au
// démarrage, les autres le résolvent lazily. Protégé par un mutex car les résolutions
// se font pendant le service des requêtes.
type registry struct {
	mu        sync.RWMutex
	providers map[string]any
}

// NewRegistry crée un registry vide. Le remplissage se fait dans cmd/api/main.go.
func NewRegistry() module.FeatureRegistry {
	return &registry{providers: make(map[string]any)}
}

// Register associe un fournisseur à une clé de module. Un réenregistrement écrase le précédent.
func (r *registry) Register(key string, provider any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[key] = provider
}

// Get renvoie le fournisseur enregistré sous key. Au consommateur de type-assert sur
// l'interface qu'il déclare de son côté.
func (r *registry) Get(key string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[key]
	return p, ok
}
