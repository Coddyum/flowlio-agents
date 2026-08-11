package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Scope              | Scope of a token: administration or a single project         | 37    |
// | Principal          | Authenticated identity carried by a request                  | 49    |
// | Principal.IsAdmin  | True if the principal may administer the team                | 57    |
// | Service            | Authentication contract exposed through CoreServices         | 62    |
// | service            | Implementation, depending on the Store interface             | 72    |
// | New                | Creates the authentication service                           | 88    |
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
	// wakeState answers "does this project principal have anything past its cursor?" from memory, so
	// the middleware can piggyback that answer onto every response (D55, DESIGN-WAKE §3). The second
	// bool is false when the answer is not known cheaply (cold cache) — the header is then omitted
	// rather than guessed. Nil disables the piggyback entirely, which is what every test double
	// wants: it keeps auth decoupled from the probe and the cache.
	wakeState func(Principal) (bool, bool)
}

// New creates the authentication service. wakeState may be nil to disable the wake piggyback.
func New(store Store, wakeState func(Principal) (bool, bool)) Service {
	return &service{
		store:         store,
		touchInterval: time.Minute,
		limiter:       newAttemptLimiter(maxAttemptsPerIP, attemptWindow),
		wakeState:     wakeState,
	}
}
