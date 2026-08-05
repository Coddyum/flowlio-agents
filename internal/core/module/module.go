package module

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                         | Ligne |
// |-----------------|----------------------------------------------------------------|-------|
// | Module          | Contrat implémenté par chaque module de feature                  | 35    |
// | CoreServices    | Services partagés transverses exposés à tous les modules         | 44    |
// | FeatureRegistry | Résolution lazy d'un module par un autre, sans import direct     | 52    |
// | ModuleConfig    | Infra partagée passée en un seul paramètre à chaque NewModule    | 59    |
// | RefScope        | The tenancy pair a reference is resolved under                   | 100   |
// | TaskRefResolver | Implemented by task: resolves a reference to a task              | 110   |
// | IssueRefResolver| Implemented by issue: resolves a reference to an issue           | 119   |
//
// Fin du sommaire.
// =====================================================================
//
// FICHIER CRITIQUE — toute modification de ces interfaces se valide avec l'humain.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/Coddyum/flowlio-agents/internal/pkg/config"
	"github.com/google/uuid"
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

// ─────────────────────────────────────────────────────────────────────────────
// Reference resolution — the first real use of FeatureRegistry in this repository.
//
// WHY THESE INTERFACES LIVE HERE AND NOT IN THE CONSUMER. A registry provider is resolved with a
// type assertion, so provider and consumer must name the SAME interface type. The consumer is
// `ref`, the providers are `task` and `issue`, and a feature may not import a feature: the only
// package all three already import is this one.
//
// WHY THE PAYLOAD IS RAW JSON AND NOT A TYPED VALUE. What a resolver returns is the owning
// feature's own API view — `taskservice.TaskDetail`, `issueservice.IssueDetail`. Naming either
// type here would drag a feature package into core and invert the dependency the whole module
// system exists to keep pointing one way. Raw JSON says exactly what crosses the boundary: bytes
// the owner has already shaped, that the carrier forwards without reading. It also costs nothing
// — `encoding/json` writes a json.RawMessage verbatim, it does not re-encode it.
//
// The two interfaces are kept apart although their signatures nearly match. A single shared one
// would let the ref service ask the wrong module for the wrong thing and still compile, on a path
// whose entire job is to tell a task from an issue.
// ─────────────────────────────────────────────────────────────────────────────

// ErrRefNotFound says that a resolver owns NOTHING under that reference. It is not a failure: it
// is the answer that lets the caller try the other resolver.
//
// Every other error is definitive and must be surfaced as-is. A resolver that returned this
// sentinel on a database outage would turn a broken instance into a plain "not found", and the
// agent reading it would conclude its reference does not exist.
var ErrRefNotFound = errors.New("module: reference not found")

// RefScope is the tenancy pair a reference is resolved under. Both values come from the token's
// Principal, never from the request body or path — that is what makes acting on behalf of another
// project impossible.
type RefScope struct {
	TeamID    uuid.UUID
	ProjectID uuid.UUID
}

// TaskRefResolver is implemented by the task module and consumed through the registry.
//
// It takes no project key: a task reference can only ever name the caller's own project, and the
// caller has already established that before asking. Tasks are a repository's internal work —
// there is no such thing as reading a sibling's.
type TaskRefResolver interface {
	// ResolveTaskRef returns the JSON body of a task and its notes, or ErrRefNotFound.
	ResolveTaskRef(ctx context.Context, scope RefScope, number int64) (json.RawMessage, error)
}

// IssueRefResolver is implemented by the issue module and consumed through the registry.
//
// It takes a project key because an issue belongs to its RECIPIENT, which is not always the
// caller. The key opens no access: visibility is decided on the token's project, inside the query.
type IssueRefResolver interface {
	// ResolveIssueRef returns the JSON body of an issue and its thread, or ErrRefNotFound.
	ResolveIssueRef(ctx context.Context, scope RefScope, projectKey string, number int64) (json.RawMessage, error)
}
