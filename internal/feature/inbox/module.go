package inbox

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | NewModule           | Câble store → service → handler et renvoie le module       | 33    |
// | mod                 | Module inbox, porteur du handler et du middleware d'auth   | 44    |
// | mod.Key             | Renvoie la clé du module                                   | 50    |
// | mod.Routes          | Déclare la route unique, middleware lié une seule fois     | 58    |
// | requireProjectScope | Refuse tout token qui n'est pas scopé à un projet          | 67    |
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

// Key identifie le module dans le FeatureRegistry et sert de préfixe à ses routes.
const Key = "inbox"

// NewModule câble la feature : store → service → handler.
//
// Le store ne reçoit PAS RawDB : l'inbox lit un état, elle n'ouvre jamais de transaction. Sa
// justesse ne dépend d'aucune atomicité — voir docs/DESIGN-M3.md.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB)
	svc := service.New(st)

	return &mod{
		h:    handler.New(svc),
		auth: cfg.Core.Auth(),
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

// Routes déclare la route unique de la feature. Le middleware est lié ICI, une seule fois.
//
// Une seule route, sans aucun paramètre : « qu'est-ce qui m'attend ». Le scope vient
// entièrement du token, y compris le curseur de lecture, qui est par token et non par projet.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	r.Handle("GET /{$}", m.auth.Middleware(requireProjectScope(http.HandlerFunc(m.h.Check))))

	return r
}

// requireProjectScope refuse tout principal qui n'est pas scopé à un projet.
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
