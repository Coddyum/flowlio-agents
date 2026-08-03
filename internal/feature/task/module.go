package task

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | NewModule          | Câble store → service → handler et renvoie le module        | 34    |
// | mod                | Module task, porteur du handler et du middleware d'auth     | 45    |
// | mod.Key            | Renvoie la clé du module                                    | 51    |
// | mod.Routes         | Déclare les routes, middleware lié une seule fois           | 62    |
// | requireProjectScope| Refuse tout token qui n'est pas scopé à un projet           | 90    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// Key identifie le module dans le FeatureRegistry et sert de préfixe à ses routes.
const Key = "task"

// NewModule câble la feature : store → service → handler.
//
// Le store reçoit RawDB en plus des queries : la création d'une tâche réserve son numéro et
// l'insère dans une seule transaction. *sql.DB s'arrête à cette couche — le service ne voit que
// l'interface store, qui expose WithTx.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB)
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

// Routes déclare les routes de la feature. Le middleware est lié ICI, une seule fois.
//
// TOUTES les routes exigent un token de portée projet. Une tâche est le travail interne d'un
// repo, géré par l'agent de ce repo : il n'existe donc aucune route qui prenne un projet en
// paramètre, et par conséquent aucune surface où un scope pourrait être contourné. Un token
// admin, qui n'est lié à aucun projet, se voit refuser l'accès plutôt que de désigner sa cible
// — administrer la tenancy et travailler dans un backlog sont deux métiers différents.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	project := func(h http.HandlerFunc) http.Handler {
		return m.auth.Middleware(requireProjectScope(h))
	}

	r.Handle("POST /{$}", project(m.h.CreateTask))
	r.Handle("GET /{$}", project(m.h.ListTasks))

	r.Handle("GET /{number}", project(m.h.GetTask))
	r.Handle("PATCH /{number}", project(m.h.UpdateTask))

	// UNE SEULE route d'écriture sur une tâche, et c'est voulu.
	//
	// Ni /notes ni /archive : la note et l'archivage sont des CHAMPS du PATCH, écrits dans la même
	// transaction que le reste. Deux chemins d'écriture pour un même objet, c'est deux surfaces à
	// sécuriser, et surtout une couture non atomique — l'agent qui archivait passait par deux
	// requêtes HTTP, et une panne entre les deux lui faisait rejouer une note déjà écrite.

	return r
}

// requireProjectScope refuse tout principal qui n'est pas scopé à un projet.
//
// Il s'enveloppe autour du Middleware d'auth et pas à l'intérieur des handlers : ajouter une
// route plus tard sans passer par `project` se verrait immédiatement dans Routes, alors qu'un
// oubli de vérification dans un handler serait invisible.
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
