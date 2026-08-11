package wake

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Wires store → service → handler and returns the module     | 34    |
// | mod                 | The wake module, holding the handler and the auth middleware| 45    |
// | mod.Key             | Returns the module key                                     | 51    |
// | mod.Routes          | Declares the probe route, middleware bound exactly once     | 59    |
// | requireProjectScope | Rejects any token that is not scoped to a project          | 73    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/wake/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/wake/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/wake/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "wake"

// NewModule wires the feature: store → service → handler.
//
// The store gets the queries but no RawDB: the probe only ever reads, and in steady state not even
// that — it answers from the shared cache. The service is the one that holds the cache, because the
// steady-state compare is coordination, not persistence.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st, cfg.Cache)

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

// Routes declares the feature's single route. The middleware is bound HERE, once.
//
// One route, no parameter: the scope is the token's, cursor included. It mirrors the inbox exactly,
// because it answers the same question in the cheap form.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	project := func(h http.HandlerFunc) http.Handler {
		return m.auth.Middleware(requireProjectScope(h))
	}

	r.Handle("GET /probe", project(m.h.Probe))
	r.Handle("POST /register", project(m.h.Register))

	return r
}

// requireProjectScope rejects any principal that is not scoped to a project.
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
