package issue

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Wires store → service → handler and returns the module     | 33    |
// | mod                 | The issue module, holding the handler and auth middleware  | 48    |
// | mod.Key             | Returns the module key                                     | 55    |
// | mod.Routes          | Declares the routes, middleware bound exactly once         | 68    |
// | requireProjectScope | Rejects any token that is not scoped to a project          | 88    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "issue"

// NewModule wires the feature: store → service → handler.
//
// The store gets RawDB: the issue, its first message and its event are written in a single
// transaction. *sql.DB stops at that layer.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB, cfg.Cache)
	svc := service.New(st, cfg.Cache)

	return &mod{
		h:    handler.New(svc),
		svc:  svc,
		auth: cfg.Core.Auth(),
	}
}

// mod holds the handler, the service and the shared auth service.
//
// The service is kept ON TOP of the handler because this module has two surfaces: HTTP, served by
// the handler, and the FeatureRegistry, served by provider.go.
type mod struct {
	h    *handler.Handler
	svc  service.Service
	auth auth.Service
}

// Key returns the module key.
func (m *mod) Key() string {
	return Key
}

// Routes declares the feature routes. The middleware is bound HERE, exactly once.
//
// Every route requires a project-scoped token: an issue is a dialogue between two repos, and an
// admin token is party to none of them.
//
// The project key appears in a reference path (`/CORE/34`) because an issue belongs to the
// recipient project, which is not always the caller's. It is NOT a scope parameter: visibility is
// decided on the token's project, inside the query. A key naming a project of which the caller is
// neither author nor recipient returns 404.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	project := func(h http.HandlerFunc) http.Handler {
		return m.auth.Middleware(requireProjectScope(h))
	}

	r.Handle("POST /{$}", project(m.h.CreateIssue))
	r.Handle("GET /{$}", project(m.h.ListIssues))

	r.Handle("GET /{project}/{number}", project(m.h.GetIssue))
	r.Handle("POST /{project}/{number}/answer", project(m.h.Answer))

	return r
}

// requireProjectScope rejects any principal that is not scoped to a project.
//
// It wraps the auth middleware rather than living inside the handlers: adding a route without
// going through the helper shows up immediately in Routes.
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
