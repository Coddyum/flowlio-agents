package bootstrap

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                 | Ligne |
// |------------------------|--------------------------------------------------------|-------|
// | Store                  | Minimal contract needed by the bootstrap                 | 35    |
// | store                  | Implementation backed by the generated queries           | 41    |
// | NewStore               | Creates the bootstrap store                              | 46    |
// | store.CountTokens      | Counts the existing tokens                               | 51    |
// | store.CreateAdminToken | Inserts the initial administration token                 | 60    |
// | EnsureAdminToken       | Creates the admin token on the very first local start-up | 77    |
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

// Store is the minimal contract of the bootstrap: count the tokens, create one.
type Store interface {
	CountTokens(ctx context.Context) (int64, error)
	CreateAdminToken(ctx context.Context, name, prefix, hash string) error
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
