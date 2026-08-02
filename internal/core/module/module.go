package module

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                         | Ligne |
// |-----------------|----------------------------------------------------------------|-------|
// | Module          | Contrat implémenté par chaque module de feature                  | 29    |
// | CoreServices    | Services partagés transverses exposés à tous les modules         | 38    |
// | FeatureRegistry | Résolution lazy d'un module par un autre, sans import direct     | 46    |
// | ModuleConfig    | Infra partagée passée en un seul paramètre à chaque NewModule    | 53    |
//
// Fin du sommaire.
// =====================================================================
//
// FICHIER CRITIQUE — toute modification de ces interfaces se valide avec l'humain.

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/core/auth"
	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/Coddyum/flowlio-ia/internal/pkg/cache"
	"github.com/Coddyum/flowlio-ia/internal/pkg/config"
)

// Module est le contrat que tout module de feature implémente. L'engine ne connaît que ça.
type Module interface {
	// Key renvoie la clé unique du module dans le FeatureRegistry et le préfixe de ses routes.
	Key() string
	// Routes renvoie le sous-routeur de la feature, monté par l'engine sous sa clé.
	Routes() http.Handler
}

// CoreServices expose les services partagés transverses (auth, billing…) à tous les modules.
// Jamais de service feature-specific ici.
type CoreServices interface {
	// Auth authentifie les requêtes et fournit le middleware lié dans chaque module.go.
	Auth() auth.Service
}

// FeatureRegistry permet à une feature d'en consommer une autre sans l'importer :
// le fournisseur s'enregistre sous sa clé, le consommateur résout lazily et type-assert
// sur une interface qu'il déclare de son côté.
type FeatureRegistry interface {
	Get(key string) (any, bool)
	Register(key string, provider any)
}

// ModuleConfig regroupe TOUTE l'infra partagée. Chaque NewModule reçoit cette struct
// et rien d'autre — jamais de dépendances en vrac en paramètres.
type ModuleConfig struct {
	DB       *database.Queries // handle des queries générées par sqlc
	RawDB    *sql.DB           // uniquement pour les transactions, via le Transactor du store
	Config   *config.Config
	Ctx      context.Context
	Cache    cache.Cache
	Core     CoreServices
	Registry FeatureRegistry
}
