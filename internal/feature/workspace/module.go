package workspace

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                          | Ligne |
// |-------------|-----------------------------------------------------------------|-------|
// | NewModule   | Câble store → service → handler et renvoie le module              | 30    |
// | mod         | Module workspace, porteur du handler et du middleware d'auth      | 42    |
// | mod.Key     | Renvoie la clé du module                                          | 48    |
// | mod.Routes  | Déclare les routes, middleware lié une seule fois                 | 57    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/core/auth"
	"github.com/Coddyum/flowlio-ia/internal/core/module"
	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/handler"
	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/store"
)

// Key identifie le module dans le FeatureRegistry et sert de préfixe à ses routes.
const Key = "workspace"

// NewModule câble la feature : store → service → handler. Chaque couche ne reçoit que
// l'interface de la précédente.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st)
	authSvc := cfg.Core.Auth()

	return &mod{
		h:    handler.New(authSvc, svc),
		auth: authSvc,
	}
}

// mod porte le handler et le service d'auth partagé.
type mod struct {
	h    *handler.Handler
	auth auth.Service
}

// Key renvoie la clé du module.
func (m *mod) Key() string {
	return Key
}

// Routes déclare les routes de la feature. Le middleware est lié ICI, une seule fois :
// aucune route ne peut être ajoutée en oubliant l'authentification sans que ça se voie.
//
// Administration (teams, projets, tokens) : portée admin obligatoire.
// Lecture des projets et whoami : tout token valide, scopé à sa propre team.
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

	r.Handle("GET /whoami", authed(http.HandlerFunc(m.h.Whoami)))

	return r
}
