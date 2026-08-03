package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                   | Ligne |
// |----------------------|----------------------------------------------------------|-------|
// | service.Authenticate | Résout un token présenté en Principal, en temps constant  | 31    |
// | service.touch        | Note l'usage du token, au plus une fois par intervalle     | 75    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// decoyHash est un hash valide comparé quand le préfixe est inconnu, pour que le chemin d'échec
// coûte le même temps que le chemin nominal. Sans lui, la latence révélerait quels préfixes
// existent en base.
var decoyHash = crypto.HashSecret("decoy")

// Authenticate résout un token présenté en Principal.
//
// Tous les échecs renvoient ErrUnauthenticated sans détail : distinguer « préfixe inconnu » de
// « secret faux » donnerait à un attaquant un moyen d'énumérer les tokens valides.
func (s *service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	prefix, secret, err := crypto.ParseToken(rawToken)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}

	rec, err := s.store.TokenByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			// Comparaison factice : le préfixe inconnu doit coûter le même temps.
			crypto.VerifySecret(secret, decoyHash)
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}

	if !crypto.VerifySecret(secret, rec.SecretHash) {
		return Principal{}, ErrUnauthenticated
	}
	if rec.Revoked {
		return Principal{}, ErrUnauthenticated
	}

	principal := Principal{
		TokenID:   rec.ID,
		Scope:     rec.Scope,
		TeamID:    rec.TeamID,
		ProjectID: rec.ProjectID,
	}

	// Un token de projet sans scope complet est une incohérence de données : le refuser plutôt
	// que servir une requête non scopée, qui contournerait l'isolation entre projets.
	if principal.Scope == ScopeProject &&
		(principal.TeamID == uuid.Nil || principal.ProjectID == uuid.Nil) {
		return Principal{}, ErrUnauthenticated
	}

	s.touch(ctx, rec)

	return principal, nil
}

// touch note l'usage du token au plus une fois par intervalle, pour ne pas transformer chaque
// lecture authentifiée en écriture. Best effort : une erreur ici ne refuse pas la requête.
func (s *service) touch(ctx context.Context, rec TokenRecord) {
	if !rec.LastUsedAt.IsZero() && time.Since(rec.LastUsedAt) < s.touchInterval {
		return
	}
	_ = s.store.TouchToken(ctx, rec.ID)
}
