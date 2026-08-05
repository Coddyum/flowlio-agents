package workspace

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                          | Ligne |
// |-------------|-----------------------------------------------------------------|-------|
// | NewModule   | Wires store → service → handler and returns the module            | 30    |
// | mod         | The workspace module, holding the handler and the auth middleware | 42    |
// | mod.Key     | Returns the module key                                            | 48    |
// | mod.Routes  | Declares the routes, middleware bound exactly once                | 57    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "workspace"

// NewModule wires the feature: store → service → handler. Each layer only ever receives the
// interface of the previous one.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st)
	authSvc := cfg.Core.Auth()

	return &mod{
		h:    handler.New(authSvc, svc),
		auth: authSvc,
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

// Routes declares the feature routes. The middleware is bound HERE, exactly once: no route can be
// added with authentication forgotten without it showing.
//
// Administration (teams, projects, tokens): admin scope required.
// Reading projects and whoami: any valid token, scoped to its own team.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	admin := m.auth.AdminOnly
	authed := m.auth.Middleware

	r.Handle("POST /teams", admin(http.HandlerFunc(m.h.CreateTeam)))
	r.Handle("GET /teams", admin(http.HandlerFunc(m.h.ListTeams)))

	r.Handle("POST /projects", admin(http.HandlerFunc(m.h.CreateProject)))
	r.Handle("GET /projects", authed(http.HandlerFunc(m.h.ListProjects)))

	r.Handle("POST /tokens", admin(http.HandlerFunc(m.h.CreateToken)))
	r.Handle("GET /tokens", admin(http.HandlerFunc(m.h.ListTokens)))
	r.Handle("DELETE /tokens/{id}", admin(http.HandlerFunc(m.h.RevokeToken)))

	// Trust graph: ADMIN on all three, no exception. An agent has full power over its own repo, so
	// a trust it declared would be self-signed by the very party it constrains. `authed` here would
	// reopen the channel that part 2 closes.
	r.Handle("GET /trust", admin(http.HandlerFunc(m.h.ListTrust)))
	r.Handle("POST /trust", admin(http.HandlerFunc(m.h.AllowTrust)))
	r.Handle("DELETE /trust/{first}/{second}", admin(http.HandlerFunc(m.h.RevokeTrust)))

	r.Handle("GET /whoami", authed(http.HandlerFunc(m.h.Whoami)))

	return r
}
