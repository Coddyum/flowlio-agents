package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                   | Ligne |
// |----------------------|----------------------------------------------------------|-------|
// | service.Authenticate | Resolves a presented token into a Principal, in constant time | 31 |
// | service.touch        | Records the token's use, at most once per interval        | 75    |
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

// decoyHash is a valid hash compared when the prefix is unknown, so that the failure path costs
// the same time as the nominal one. Without it, the latency would reveal which prefixes
// exist in the database.
var decoyHash = crypto.HashSecret("decoy")

// Authenticate resolves a presented token into a Principal.
//
// Every failure yields ErrUnauthenticated with no detail: telling "unknown prefix" apart from
// "wrong secret" would give an attacker a way to enumerate the valid tokens.
func (s *service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	prefix, secret, err := crypto.ParseToken(rawToken)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}

	rec, err := s.store.TokenByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			// Decoy comparison: an unknown prefix must cost the same time.
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

	// A project token without a complete scope is a data inconsistency: reject it rather than
	// serve an unscoped request, which would bypass the isolation between projects.
	if principal.Scope == ScopeProject &&
		(principal.TeamID == uuid.Nil || principal.ProjectID == uuid.Nil) {
		return Principal{}, ErrUnauthenticated
	}

	s.touch(ctx, rec)

	return principal, nil
}

// touch records the token's use at most once per interval, so as not to turn every
// authenticated read into a write. Best effort: an error here does not reject the request.
func (s *service) touch(ctx context.Context, rec TokenRecord) {
	if !rec.LastUsedAt.IsZero() && time.Since(rec.LastUsedAt) < s.touchInterval {
		return
	}
	_ = s.store.TouchToken(ctx, rec.ID)
}
