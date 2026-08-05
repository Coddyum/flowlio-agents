package ref

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Wires store → service → handler and returns the module     | 51    |
// | mod                 | The ref module: its handler and the shared auth service    | 62    |
// | mod.Key             | Returns the module's key                                   | 72    |
// | mod.Routes          | Declares the single route, middleware bound once           | 85    |
// | requireProjectScope | Refuses any token that is not scoped to a project          | 97    |
//
// Fin du sommaire.
// =====================================================================
//
// THE FIRST MODULE THAT CONSUMES ANOTHER, AND THE PATTERN THE NEXT ONES WILL COPY.
//
// It owns no table. It answers one question — "what is CORE-34?" — by asking `task`, then
// `issue`, through the FeatureRegistry. Nothing here imports either of them: the coupling is a
// string key and an interface declared in internal/core/module, which is the only package all
// three already share.
//
// WHY IT IS A MODULE AND NOT A QUERY. A single SQL query answering both would have been a third
// of the code — and it would have read `tasks` AND `issues` from one feature, making one domain
// name another's tables. That is the one flow rule this repository has never bent, and the price
// of bending it is not one query: it is the precedent.
//
// WHY IT HAS NO WRITE PATH, AND MUST NOT GROW ONE. Composing two features is safe precisely
// because nothing here decides anything — it forwards a scope and picks which peer to ask. A
// write on this surface would be a mutation of someone else's domain, reached through a module
// that has no query of its own to carry the scope.

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "ref"

// NewModule wires the feature: store → service → handler.
//
// The store gets no RawDB: this surface reads, and reads only. The registry is handed to the
// SERVICE and resolved at request time — modules are built before any of them is registered, so
// a peer captured here would always be nil.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st, cfg.Registry)

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

// Key returns the module's key.
//
// It is also what this module does NOT register anything under: `ref` is a consumer, never a
// provider. Registering it would offer the composition back to the very features it composes,
// and the first cycle would only show up at request time.
func (m *mod) Key() string {
	return Key
}

// Routes declares the feature's single route. The middleware is bound HERE, once.
//
// The project key sits in the path (`/CORE/34`) because a reference can name a sibling — an issue
// belongs to its recipient, which is not always the caller. It is NOT a scope parameter: the scope
// comes from the token, and each peer re-applies it in its own query.
//
// A project-scoped token is required, like every surface an agent works through. An admin token
// is refused rather than made to designate a target: a reference is read FROM a project, and a
// principal without one has none to read from.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	r.Handle("GET /{project}/{number}", m.auth.Middleware(requireProjectScope(http.HandlerFunc(m.h.GetRef))))

	return r
}

// requireProjectScope refuses any principal that is not scoped to a project.
//
// It wraps the auth middleware rather than living inside the handler: adding a route later
// without the guard would be visible in Routes, whereas a missing check inside a handler is not.
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
