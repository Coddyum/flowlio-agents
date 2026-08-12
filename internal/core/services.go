package core

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | services         | Carries the shared cross-cutting services exposed to modules  | 24    |
// | NewServices      | Instantiates the shared services from the process infra       | 34    |
// | services.Auth    | Yields the shared authentication service                      | 49    |
//
// Fin du sommaire.
// =====================================================================

import (
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
)

// services implements module.CoreServices. It carries nothing but cross-cutting concerns, never
// the service of a specific feature.
type services struct {
	auth auth.Service
}

// NewServices instantiates the shared services. Called once, in main.
//
// It hands the auth layer the wake piggyback probe: a closure over the SAME cache the features write
// their probe signals into, so every authenticated project response can carry "you have something
// past your cursor" read straight from memory (D55, DESIGN-WAKE §3). Auth stays decoupled from the
// probe and the cache — it only ever sees the closure.
func NewServices(q *database.Queries, c cache.Cache) module.CoreServices {
	wakeState := func(p auth.Principal) (bool, bool) {
		head, headWarm := probe.Head(c, p.TeamID, p.ProjectID)
		cursor, cursorWarm := probe.Cursor(c, p.TokenID)
		if !headWarm || !cursorWarm {
			return false, false
		}
		return head > cursor, true
	}
	return &services{
		auth: auth.New(auth.NewStore(q), wakeState),
	}
}

// Auth yields the authentication service shared by every module.
func (s *services) Auth() auth.Service {
	return s.auth
}
