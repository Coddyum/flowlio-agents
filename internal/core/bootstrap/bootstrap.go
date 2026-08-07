package bootstrap

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                 | Ligne |
// |------------------------|--------------------------------------------------------|-------|
// | StoredToken            | What a registered token reveals: scope, hash, revocation | 60    |
// | Store                  | Minimal contract needed by the local bootstrap           | 68    |
// | HostedStore            | Contract of the hosted bootstrap: look up, insert        | 82    |
// | store                  | Implementation backed by the generated queries           | 89    |
// | NewStore               | Creates the bootstrap store                              | 94    |
// | NewHostedStore         | Creates the store of the hosted bootstrap                | 99    |
// | store.AdminTokenByPrefix| Yields the row a prefix points at, judging nothing       | 109   |
// | store.CountTokens      | Counts the existing tokens                               | 125   |
// | store.CreateAdminToken | Inserts an administration token                          | 134   |
// | store.RevokeAdminTokens| Revokes every live administration token                  | 147   |
// | RotateAdminToken       | Replaces the admin token once it has been lost           | 168   |
// | EnsureAdminToken       | Creates the admin token on the very first local start-up | 190   |
// | EnsureHostedAdminToken | Registers the token a hosted instance was configured with| 222   |
//
// Fin du sommaire.
// =====================================================================
//
// Local mode: no account, no password. On the very first start-up, the server issues one single
// administration token, writes it into the user's credentials file and prints it once. That token
// is what the CLI uses to create the first team.
//
// HOSTED MODE IS THE MIRROR IMAGE, and it took until 2026-08-07 to notice it was missing. There the
// server issues nothing: the secret is minted by the operator, put into the deployment's secret
// store, and this bootstrap only registers its hash. The reason is not symmetry but arithmetic —
// the process that needs to PRESENT that token is the co-deployed flowlio-core, a sibling process,
// and a secret this server generated at start-up has no way of reaching it. Before that, a fresh
// hosted database held zero tokens and there was no code path anywhere that could add one.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

const (
	// adminTokenName identifies the token created at bootstrap.
	adminTokenName = "local bootstrap"
	// hostedAdminTokenName identifies the token the hosted operator supplied.
	hostedAdminTokenName = "hosted operator"
	// adminScope is the scope a registered administration token must carry, as the database
	// spells it.
	adminScope = "admin"
)

// ErrNoSuchPrefix says the prefix matches no row at all — the token has never been registered.
var ErrNoSuchPrefix = errors.New("bootstrap: no token with that prefix")

// StoredToken is the part of a token row the hosted bootstrap has to judge. The secret itself is
// not among them, and cannot be: the database only ever kept a hash.
type StoredToken struct {
	Scope      string
	SecretHash string
	Revoked    bool
}

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

// HostedStore is what the hosted bootstrap needs, and deliberately not what Store holds: it never
// counts and never revokes. Registering a token the operator already owns is an insert at most,
// and an interface that could revoke would invite a start-up that quietly cuts off whoever was
// using the instance a second ago.
type HostedStore interface {
	// AdminTokenByPrefix yields the row a prefix points at, or ErrNoSuchPrefix.
	AdminTokenByPrefix(ctx context.Context, prefix string) (StoredToken, error)
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

// NewHostedStore creates the store of the hosted bootstrap.
func NewHostedStore(q *database.Queries) HostedStore {
	return &store{q: q}
}

// AdminTokenByPrefix yields the row a prefix points at, or ErrNoSuchPrefix when there is none.
//
// The lookup filters on neither the scope nor revoked_at, and the caller judges both. A query that
// filtered would make "registered under another scope" and "never registered" the same answer, and
// the hosted bootstrap would then happily insert a second row on a prefix a project token already
// holds.
func (s *store) AdminTokenByPrefix(ctx context.Context, prefix string) (StoredToken, error) {
	row, err := s.q.GetTokenByPrefix(ctx, prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredToken{}, ErrNoSuchPrefix
	}
	if err != nil {
		return StoredToken{}, fmt.Errorf("bootstrap store: token by prefix: %w", err)
	}
	return StoredToken{
		Scope:      string(row.Scope),
		SecretHash: row.SecretHash,
		Revoked:    row.RevokedAt.Valid,
	}, nil
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

// EnsureHostedAdminToken registers the administration token a hosted instance was configured with,
// and yields nothing: the secret came in from the environment and leaves through no other door.
//
// Idempotent by construction — a redeploy re-runs it on every start, so "already registered with
// this very secret" has to be a silent success, not a second row.
//
// Every other outcome is a REFUSAL AT START-UP rather than a warning, and that is the point of
// doing this here. A hosted instance whose administration token is malformed, revoked, or held by
// another scope is an instance that will answer 401 to its own operator; saying so in front of the
// person watching the deploy costs a minute, and finding it later costs an outage. None of the
// messages carries the secret, only what is wrong with it.
func EnsureHostedAdminToken(ctx context.Context, st HostedStore, plain string) error {
	prefix, secret, err := crypto.ParseToken(plain)
	if err != nil {
		return fmt.Errorf("bootstrap: ADMIN_TOKEN is not a flowlio token: %w", err)
	}

	existing, err := st.AdminTokenByPrefix(ctx, prefix)
	switch {
	case errors.Is(err, ErrNoSuchPrefix):
		return st.CreateAdminToken(ctx, hostedAdminTokenName, prefix, crypto.HashSecret(secret))
	case err != nil:
		return err
	}

	switch {
	case existing.Scope != adminScope:
		return fmt.Errorf("bootstrap: ADMIN_TOKEN is registered as a %q token, not an administration one",
			existing.Scope)
	case existing.Revoked:
		return errors.New("bootstrap: ADMIN_TOKEN has been revoked — mint another one and " +
			"replace it in the environment")
	case !crypto.VerifySecret(secret, existing.SecretHash):
		// Two different secrets sharing a 12-character prefix is not a coincidence anyone should
		// paper over: either ADMIN_TOKEN was edited by hand, or it belongs to another instance.
		return errors.New("bootstrap: ADMIN_TOKEN's prefix is already registered with a different secret")
	}
	return nil
}
