package inbox

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Wires store → service → handler and returns the module     | 33    |
// | mod                 | Inbox module, carrying the handler and the auth middleware | 44    |
// | mod.Key             | Returns the module key                                     | 50    |
// | mod.Routes          | Declares the single route, middleware bound once           | 58    |
// | requireProjectScope | Rejects any token that is not scoped to a project          | 67    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "inbox"

// NewModule wires the feature: store → service → handler.
//
// The store does NOT receive RawDB: the inbox reads a state, it never opens a transaction. Its
// correctness rests on no atomicity at all — see docs/DESIGN-M3.md.
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
// One route, with no parameter at all: "what is waiting for me". The scope comes entirely from
// the token, including the read cursor, which is per token and not per project.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	r.Handle("GET /{$}", m.auth.Middleware(requireProjectScope(http.HandlerFunc(m.h.Check))))

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
