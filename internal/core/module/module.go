package module

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                         | Ligne |
// |-----------------|----------------------------------------------------------------|-------|
// | Module          | Contract implemented by every feature module                     | 35    |
// | CoreServices    | Shared cross-cutting services exposed to every module            | 44    |
// | FeatureRegistry | Lazy resolution of one module by another, with no direct import  | 52    |
// | ModuleConfig    | Shared infra passed as a single parameter to every NewModule     | 59    |
// | RefScope        | The tenancy pair a reference is resolved under                   | 100   |
// | TaskRefResolver | Implemented by task: resolves a reference to a task              | 110   |
// | IssueRefResolver| Implemented by issue: resolves a reference to an issue           | 119   |
//
// Fin du sommaire.
// =====================================================================
//
// CRITICAL FILE — any change to these interfaces is validated with the human.

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

// Module is the contract every feature module implements. The engine knows nothing else.
type Module interface {
	// Key yields the module's unique key in the FeatureRegistry and the prefix of its routes.
	Key() string
	// Routes yields the feature's sub-router, mounted by the engine under its key.
	Routes() http.Handler
}

// CoreServices exposes the shared cross-cutting services (auth, billing…) to every module.
// Jamais de service feature-specific ici.
type CoreServices interface {
	// Auth authenticates the requests and provides the middleware bound in each module.go.
	Auth() auth.Service
}

// FeatureRegistry lets one feature consume another without importing it: the provider registers
// itself under its key, the consumer resolves lazily and type-asserts on an interface it declares
// on its own side.
type FeatureRegistry interface {
	Get(key string) (any, bool)
	Register(key string, provider any)
}

// ModuleConfig gathers ALL the shared infra. Every NewModule receives this struct and nothing
// else — never loose dependencies as parameters.
type ModuleConfig struct {
	DB       *database.Queries // handle on the sqlc-generated queries
	RawDB    *sql.DB           // for transactions only, through the store's Transactor
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
