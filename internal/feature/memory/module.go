package memory

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                     | Ligne |
// |---------------------|------------------------------------------------------------|-------|
// | NewModule           | Wires store → service → handler and returns the module       | 48    |
// | mod                 | The memory module, holding the handler and the auth service  | 59    |
// | mod.Key             | Returns the module key                                       | 65    |
// | mod.Routes          | Declares the routes, middleware bound exactly once           | 80    |
// | requireProjectScope | Rejects any token that is not scoped to a project            | 109   |
//
// Fin du sommaire.
// =====================================================================
//
// SEVENTH MODULE — what a repository remembers about itself. M5 (FLWL-7).
//
// SCOPE: THE REPOSITORY, AND NOTHING ELSE. Read and write by the project's token, no crossing,
// ever. The team-wide memory was dropped on 2026-08-05, and dropping it is what made this card
// deliverable: a shared memory an agent reads as instructions is an injection channel between
// repositories, and it would have needed the `mcp_untrusted.go` framing on its read path plus the
// author of every entry. In repository scope an agent only ever reads what its own repository
// wrote, so there is nothing to frame.
//
// NO ROUTE OF THIS MODULE IS REACHABLE BY AN ADMIN TOKEN, and none must ever be. `overview` grants
// itself a team-wide read over tasks and issues because a supervisor reads a debt in order to act
// on it. A memory is a repository talking to its own future sessions; opening it to a third party
// turns a private note into the exact channel this design refused.

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "memory"

// NewModule wires the feature: store → service → handler.
//
// The store gets RawDB on top of the queries: writing an entry inserts it, charges the quota and
// retires what it replaces, all in one transaction. *sql.DB stops at that layer — the service only
// ever sees the store interface, which exposes WithTx.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB)
	svc := service.New(st)

	return &mod{
		h:    handler.New(svc),
		auth: cfg.Core.Auth(),
	}
}

// mod holds the handler and the shared auth service.
type mod struct {
	h    *handler.Handler
	auth auth.Service
}

// Key returns the module key.
func (m *mod) Key() string {
	return Key
}

// Routes declares the feature routes. The middleware is bound HERE, exactly once.
//
// EVERY ROUTE IS PROJECT-SCOPED, WRITES AND READS ALIKE, and there is no mixed gate. Under plain
// `Middleware` an admin token would reach these routes and read a repository's private reasoning
// without being that repository — and the existing isolation tests would stay GREEN, because they
// check that the `task` and `issue` queries are scoped, not that a seventh module exists.
//
// THERE IS NO DELETE AND NO PATCH, and neither is an omission. An entry is never edited and never
// erased: it is SUPERSEDED, through the `supersedes` field of a write. That is the one thing this
// table offers over a markdown file — "why was it like that" stays answerable alongside "why is it
// like this" — and a PATCH would quietly take it away.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	project := func(h http.HandlerFunc) http.Handler {
		return m.auth.Middleware(requireProjectScope(h))
	}

	r.Handle("POST /{$}", project(m.h.Remember))
	r.Handle("GET /{$}", project(m.h.Recall))

	// The index is a route of its own rather than a parameter of the listing: it is read once per
	// session by the MCP handshake, it carries titles and no bodies, and its bound is much tighter.
	// Folding it into `GET /` as a flag would have one call serve two budgets, and the tighter of
	// the two would eventually be relaxed to suit the other.
	r.Handle("GET /index", project(m.h.Index))

	// After /index, so the literal segment wins over the wildcard. Go's ServeMux picks the most
	// specific pattern regardless of registration order, but the reading order matters to whoever
	// audits this list.
	r.Handle("GET /{slug}", project(m.h.Get))

	return r
}

// requireProjectScope rejects any token that is not scoped to a project — an admin token included.
//
// A local middleware and not a shared one: it is the exact twin of the guards in `task`, `issue`,
// `inbox` and `ref`, and the repository keeps them local on purpose. A shared helper would be one
// place to relax for all five at once.
func requireProjectScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.FromContext(r.Context())
		if !ok || principal.Scope != auth.ScopeProject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
