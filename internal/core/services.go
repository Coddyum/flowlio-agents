package core

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | services         | Porte les services partagés transverses exposés aux modules   | 22    |
// | NewServices      | Instancie les services partagés à partir de l'infra du process| 27    |
// | services.Auth    | Renvoie le service d'authentification partagé                 | 34    |
//
// Fin du sommaire.
// =====================================================================

import (
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
)

// services implémente module.CoreServices. Il ne porte que du transverse, jamais le service
// d'une feature précise.
type services struct {
	auth auth.Service
}

// NewServices instancie les services partagés. Appelé une seule fois, dans main.
func NewServices(q *database.Queries) module.CoreServices {
	return &services{
		auth: auth.New(auth.NewStore(q)),
	}
}

// Auth renvoie le service d'authentification partagé par tous les modules.
func (s *services) Auth() auth.Service {
	return s.auth
}
