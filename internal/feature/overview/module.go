package overview

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                            | Ligne |
// |------------|-------------------------------------------------------------------|-------|
// | NewModule  | Câble store → service → handler et renvoie le module                | 47    |
// | mod        | Module overview, porteur du handler et du middleware d'auth         | 58    |
// | mod.Key    | Renvoie la clé du module                                            | 64    |
// | mod.Routes | Déclare les deux routes, toutes deux derrière AdminOnly             | 78    |
//
// Fin du sommaire.
// =====================================================================
//
// CINQUIÈME MODULE, ET NON UNE EXTENSION D'INBOX. `inbox` scope par `project_id`, `overview` par
// `team_id` SEUL. Les voisiner dans un même store est la configuration exacte où le copier-coller
// fuit : deux queries adjacentes, deux règles de scope différentes, une revue qui lit vite.
//
// CE MODULE NE LIT QUE, ET NE POSSÈDE RIEN. Il lit `teams`, `projects`, `tokens`, `issues`,
// `issue_messages`, `tasks` et `task_notes` — sept tables dont six appartiennent à d'autres
// domaines. Ce n'est pas un import inter-feature : la décision M3 #26 autorise une feature à LIRE
// la table d'un autre domaine par une query scopée dédiée, et interdit d'y ÉCRIRE. Ici rien
// n'écrit, jamais : zéro import Go vers une autre feature, et `check-overview-scope.sh` refuse
// tout INSERT/UPDATE/DELETE dans `sql/queries/overview.sql`.
//
// AUCUNE DE CES ROUTES N'EST EXPOSÉE EN MCP, et il ne faut pas l'y exposer. Un agent qui lit
// l'état de sa team entière détruit la promesse d'isolation du produit — en lecture, sans qu'un
// seul test de tenancy ne tombe.

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
)

// Key identifie le module dans le FeatureRegistry et sert de préfixe à ses routes.
const Key = "overview"

// NewModule câble la feature : store → service → handler.
//
// Le store ne reçoit PAS RawDB : cette surface est en lecture seule, elle n'ouvre jamais de
// transaction et n'a aucune atomicité à garantir.
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

// Routes déclare les deux routes de la feature. Le middleware est lié ICI, une seule fois, et
// c'est `AdminOnly` — jamais `Middleware`.
//
// LA DIFFÉRENCE N'EST PAS COSMÉTIQUE. Sous `auth.Middleware`, un token de projet atteindrait ces
// routes et lirait le fil d'une conversation entre deux repos frères dont il n'est ni l'auteur ni
// le destinataire. Les huit tests d'isolation existants resteraient VERTS : ils vérifient que les
// queries de `task` et `issue` sont scopées, pas qu'aucune autre route ne contourne ce scope.
//
// Il n'y a pas de gate mixte, et il ne faut pas en introduire : les deux routes sont admin, ou
// aucune ne l'est.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	admin := m.auth.AdminOnly

	r.Handle("GET /{$}", admin(http.HandlerFunc(m.h.TeamState)))
	r.Handle("GET /refs/{project}/{number}", admin(http.HandlerFunc(m.h.RefDetail)))

	return r
}
