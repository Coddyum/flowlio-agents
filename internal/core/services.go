package core

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | services         | Carries the shared cross-cutting services exposed to modules  | 22    |
// | NewServices      | Instantiates the shared services from the process infra       | 27    |
// | services.Auth    | Yields the shared authentication service                      | 34    |
//
// Fin du sommaire.
// =====================================================================

import (
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
)

// services implements module.CoreServices. It carries nothing but cross-cutting concerns, never
// the service of a specific feature.
type services struct {
	auth auth.Service
}

// NewServices instantiates the shared services. Called once, in main.
func NewServices(q *database.Queries) module.CoreServices {
	return &services{
		auth: auth.New(auth.NewStore(q)),
	}
}

// Auth yields the authentication service shared by every module.
func (s *services) Auth() auth.Service {
	return s.auth
}
