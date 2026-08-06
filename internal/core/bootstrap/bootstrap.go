package bootstrap

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                 | Ligne |
// |------------------------|--------------------------------------------------------|-------|
// | Store                  | Minimal contract needed by the bootstrap                 | 38    |
// | store                  | Implementation backed by the generated queries           | 49    |
// | NewStore               | Creates the bootstrap store                              | 54    |
// | store.CountTokens      | Counts the existing tokens                               | 59    |
// | store.CreateAdminToken | Inserts the initial administration token                 | 68    |
// | store.RevokeAdminTokens| Revokes every live administration token                  | 81    |
// | RotateAdminToken       | Replaces the admin token once it has been lost           | 102   |
// | EnsureAdminToken       | Creates the admin token on the very first local start-up | 124   |
//
// Fin du sommaire.
// =====================================================================
//
// Local mode: no account, no password. On the very first start-up, the server issues one single
// administration token, writes it into the user's credentials file and prints it once. That token
// is what the CLI uses to create the first team.
//
// In hosted mode this bootstrap never runs: admin tokens there follow from an account.

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

// adminTokenName identifies the token created at bootstrap.
const adminTokenName = "local bootstrap"

// Store is the minimal contract of the bootstrap: count the tokens, create one, and revoke the
// administration ones when they are being rotated.
type Store interface {
	CountTokens(ctx context.Context) (int64, error)
	CreateAdminToken(ctx context.Context, name, prefix, hash string) error

	// RevokeAdminTokens revokes every live admin token and returns how many there were. No
	// identifier is named because none can be: the server keeps a hash, so the operator cannot
	// designate the token they lost.
	RevokeAdminTokens(ctx context.Context) (int64, error)
}

// store backs the contract with the generated queries.
type store struct {
	q *database.Queries
}

// NewStore creates the bootstrap store.
func NewStore(q *database.Queries) Store {
	return &store{q: q}
}

// CountTokens counts the existing tokens, across every scope.
func (s *store) CountTokens(ctx context.Context) (int64, error) {
	n, err := s.q.CountTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("bootstrap store: count tokens: %w", err)
	}
	return n, nil
}

// CreateAdminToken inserts the initial administration token.
func (s *store) CreateAdminToken(ctx context.Context, name, prefix, hash string) error {
	_, err := s.q.CreateAdminToken(ctx, database.CreateAdminTokenParams{
		Name:       name,
		Prefix:     prefix,
		SecretHash: hash,
	})
	if err != nil {
		return fmt.Errorf("bootstrap store: create admin token: %w", err)
	}
	return nil
}

// RevokeAdminTokens revokes every live administration token and returns how many there were.
func (s *store) RevokeAdminTokens(ctx context.Context) (int64, error) {
	revoked, err := s.q.RevokeAdminTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("bootstrap store: revoke admin tokens: %w", err)
	}
	return revoked, nil
}

// RotateAdminToken revokes every live administration token, then issues a new one. It returns the
// new secret and how many tokens it replaced.
//
// This is the ONLY way back in once the admin token is lost, and the loss is not a hypothesis: it
// was lived through on this very repository. The server keeps a hash and nothing else, and the
// first-run bootstrap issues nothing as long as the table holds a token — so a lost credential
// leaves an installation that answers 401 to its own operator, forever, with the database as the
// only way in. That is acceptable on a laptop. It is not on an installation someone else runs.
//
// Revoking first is what makes it a rotation: the lost token stops being live. Both writes are
// deliberately NOT in a transaction — should the creation fail after the revocation, the
// installation is left with no admin token at all, which the first-run bootstrap knows how to fix
// on the next start. The reverse order has no such recovery.
func RotateAdminToken(ctx context.Context, st Store) (string, int64, error) {
	revoked, err := st.RevokeAdminTokens(ctx)
	if err != nil {
		return "", 0, err
	}

	token, err := crypto.NewToken()
	if err != nil {
		return "", 0, fmt.Errorf("bootstrap: generating the admin token: %w", err)
	}
	if err := st.CreateAdminToken(ctx, adminTokenName, token.Prefix, token.Hash); err != nil {
		return "", 0, err
	}

	return token.Plain, revoked, nil
}

// EnsureAdminToken issues the administration token if and only if the database holds none. The
// secret it yields exists only here: it cannot be read back afterwards.
//
// The second return says whether a token was just created; false means the installation was
// already bootstrapped and there is nothing to print.
func EnsureAdminToken(ctx context.Context, st Store) (string, bool, error) {
	count, err := st.CountTokens(ctx)
	if err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}

	token, err := crypto.NewToken()
	if err != nil {
		return "", false, fmt.Errorf("bootstrap: generating the admin token: %w", err)
	}

	if err := st.CreateAdminToken(ctx, adminTokenName, token.Prefix, token.Hash); err != nil {
		return "", false, err
	}

	return token.Plain, true, nil
}
