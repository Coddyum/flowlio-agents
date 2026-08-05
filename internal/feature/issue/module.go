package issue

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Câble store → service → handler et renvoie le module       | 33    |
// | mod                 | Module issue, porteur du handler et du middleware d'auth   | 48    |
// | mod.Key             | Renvoie la clé du module                                   | 55    |
// | mod.Routes          | Déclare les routes, middleware lié une seule fois          | 68    |
// | requireProjectScope | Refuse tout token qui n'est pas scopé à un projet          | 88    |
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

// Key identifie le module dans le FeatureRegistry et sert de préfixe à ses routes.
const Key = "issue"

// NewModule câble la feature : store → service → handler.
//
// Le store reçoit RawDB : l'issue, son premier message et son événement s'écrivent dans une
// seule transaction. *sql.DB s'arrête à cette couche.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB)
	svc := service.New(st)

	return &mod{
		h:    handler.New(svc),
		svc:  svc,
		auth: cfg.Core.Auth(),
	}
}

// mod porte le handler, le service et le service d'auth partagé.
//
// Le service est retenu EN PLUS du handler parce que ce module a deux surfaces : HTTP, servie par
// le handler, et le FeatureRegistry, servie par provider.go.
type mod struct {
	h    *handler.Handler
	svc  service.Service
	auth auth.Service
}

// Key renvoie la clé du module.
func (m *mod) Key() string {
	return Key
}

// Routes déclare les routes de la feature. Le middleware est lié ICI, une seule fois.
//
// Toutes les routes exigent un token de portée projet : une issue est un dialogue entre deux
// repos, un token admin n'en est partie prenante d'aucun.
//
// La clé de projet figure dans le chemin d'une référence (`/CORE/34`) parce qu'une issue
// appartient au projet destinataire, qui n'est pas toujours celui de l'appelant. Ce n'est PAS un
// paramètre de scope : la visibilité se décide sur le projet du token, dans la query. Une clé
// désignant un projet dont l'appelant n'est ni auteur ni destinataire remonte 404.
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

// requireProjectScope refuse tout principal qui n'est pas scopé à un projet.
//
// Il enveloppe le Middleware d'auth plutôt que de vivre dans les handlers : ajouter une route
// sans passer par le helper se verrait immédiatement dans Routes.
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
