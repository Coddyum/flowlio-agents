package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Scope              | Scope of a token: administration or a single project         | 38    |
// | Principal          | Authenticated identity carried by a request                  | 50    |
// | Principal.IsAdmin  | True if the principal may administer the team                | 58    |
// | Service            | Authentication contract exposed through CoreServices         | 63    |
// | service            | Implementation, depending on the Store interface             | 73    |
// | New                | Creates the authentication service                           | 83    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in authenticate.go, middleware.go and rate_limit.go.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ErrUnauthenticated covers EVERY authentication failure: unknown prefix, wrong secret, revoked
// or malformed token. A caller must never be able to tell those cases apart — that would be an
// oracle letting the valid tokens be enumerated.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// ErrForbidden signals an authenticated principal whose scope does not cover the resource.
var ErrForbidden = errors.New("auth: forbidden")

// Scope describes what a token is allowed to do.
type Scope string

const (
	// ScopeAdmin administers the teams and the projects. Issued at bootstrap in local mode.
	ScopeAdmin Scope = "admin"
	// ScopeProject is an agent's token: one single project, within one single team.
	ScopeProject Scope = "project"
)

// Principal is the authenticated identity of a request. TeamID and ProjectID are empty for an
// admin token; they are always set for a project token, and it is that pair which scopes every
// query of the store.
type Principal struct {
	TokenID   uuid.UUID
	Scope     Scope
	TeamID    uuid.UUID
	ProjectID uuid.UUID
}

// IsAdmin says whether the principal can create teams, projects and tokens.
func (p Principal) IsAdmin() bool {
	return p.Scope == ScopeAdmin
}

// Service authenticates the requests. Exposed to every module through CoreServices.Auth().
type Service interface {
	// Authenticate resolves a presented token into a Principal, or yields ErrUnauthenticated.
	Authenticate(ctx context.Context, rawToken string) (Principal, error)
	// Middleware requires a valid token and puts the Principal into the request context.
	Middleware(next http.Handler) http.Handler
	// AdminOnly additionally requires a token of admin scope.
	AdminOnly(next http.Handler) http.Handler
}

// service depends on the Store interface, never on sqlc.
type service struct {
	store Store
	// touchInterval bounds how often last_used_at is written: without it, every authenticated
	// request would trigger an UPDATE.
	touchInterval time.Duration
	// limiter slows prefix sweeping down. Detail and trade-offs: rate_limit.go.
	limiter *attemptLimiter
}

// New creates the authentication service.
func New(store Store) Service {
	return &service{
		store:         store,
		touchInterval: time.Minute,
		limiter:       newAttemptLimiter(maxAttemptsPerIP, attemptWindow),
	}
}
