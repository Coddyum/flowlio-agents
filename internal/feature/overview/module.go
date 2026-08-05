package overview

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                            | Ligne |
// |------------|-------------------------------------------------------------------|-------|
// | NewModule  | Wires store → service → handler and returns the module              | 47    |
// | mod        | Overview module, carrying the handler and the auth middleware       | 58    |
// | mod.Key    | Returns the module key                                              | 64    |
// | mod.Routes | Declares the two routes, both of them behind AdminOnly              | 77    |
//
// Fin du sommaire.
// =====================================================================
//
// FIFTH MODULE, AND NOT AN EXTENSION OF INBOX. `inbox` scopes by `project_id`, `overview` by
// `team_id` ALONE. Sitting them side by side in one store is the exact setup where copy-paste
// leaks: two adjacent queries, two different scoping rules, one review that reads fast.
//
// THIS MODULE ONLY READS, AND OWNS NOTHING. It reads `teams`, `projects`, `tokens`, `issues`,
// `issue_messages`, `tasks` and `task_notes` — seven tables, six of which belong to other
// domains. This is not an inter-feature import: decision M3 #26 lets a feature READ another
// domain's table through a dedicated scoped query, and forbids WRITING to it. Here nothing
// writes, ever: zero Go import towards another feature, and `check-overview-scope.sh` rejects any
// INSERT/UPDATE/DELETE in `sql/queries/overview.sql`.
//
// NONE OF THESE ROUTES IS EXPOSED OVER MCP, and none must be. An agent reading the state of its
// whole team destroys the product's isolation promise — through reads, without a single tenancy
// test falling over.

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "overview"

// NewModule wires the feature: store → service → handler.
//
// The store does NOT receive RawDB: this surface is read-only, it never opens a transaction and
// has no atomicity to guarantee.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st)

	return &mod{
		h:    handler.New(svc),
		auth: cfg.Core.Auth(),
	}
}

// mod carries the handler and the shared auth service.
type mod struct {
	h    *handler.Handler
	auth auth.Service
}

// Key returns the module key.
func (m *mod) Key() string {
	return Key
}

// Routes declares the feature's two routes. The middleware is bound HERE, once, and it is
// `AdminOnly` — never `Middleware`.
//
// THE DIFFERENCE IS NOT COSMETIC. Under `auth.Middleware`, a project token would reach these
// routes and read the thread of a conversation between two sibling repos it is neither the author
// nor the recipient of. The eight existing isolation tests would stay GREEN: they check that the
// `task` and `issue` queries are scoped, not that no other route bypasses that scope.
//
// There is no mixed gate, and none must be introduced: both routes are admin, or neither is.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	admin := m.auth.AdminOnly

	r.Handle("GET /{$}", admin(http.HandlerFunc(m.h.TeamState)))
	r.Handle("GET /refs/{project}/{number}", admin(http.HandlerFunc(m.h.RefDetail)))

	return r
}
