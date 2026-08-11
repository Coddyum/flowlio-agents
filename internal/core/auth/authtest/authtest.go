// Package authtest provides the harness that lets a route test present a token whose scope it
// chooses.
//
// WHY THIS PACKAGE EXISTS. `auth.contextKey` is private: no package outside `internal/core/auth`
// can put a `Principal` into a request context. That is a good thing — a route test MUST exercise
// the real authentication chain, otherwise it proves the tenancy of its own double. But every
// module testing a route therefore has to build a fake `auth.Store` and mint a real token, and
// that has already happened twice in this repo.
//
// A `*test` package is the only clean way in Go to expose a double without letting it into the
// binary: nothing imports it outside the `_test.go`, and a guardrail checks it
// (scripts/check-authtest-not-in-production.sh).
//
// WHAT IT DOES NOT DO, DELIBERATELY. It builds no `Principal` directly and exposes no shortcut to
// inject one. The only path is the real middleware, on a real token minted by `crypto.NewToken()`.
// A helper short-circuiting the authentication would turn green tests that an auth regression
// ought to make fall over.
package authtest

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                             | Ligne |
// |--------------|--------------------------------------------------------------------|-------|
// | Store        | Fake auth.Store, which checks the prefix presented to it            | 51    |
// | Store.TokenByPrefix | Yields the test's token if the prefix matches                | 57    |
// | Store.TouchToken    | Records the use, with no effect                              | 66    |
// | Token        | A minted token and the auth service that recognises it              | 73    |
// | New          | Mints a token of the requested scope and mounts the auth service    | 90    |
// | Admin        | Admin token, optionally pinned to a team                            | 117   |
// | Project      | Agent token, scoped to a team and a project                         | 123   |
// | Token.Authorize | Sets the Authorization header on a request                       | 134   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// Store is an auth.Store that knows one single token.
//
// It CHECKS the presented prefix, and that is the point that counts: a double yielding its token
// whatever it is asked would make a middleware that does not extract the prefix look correct.
type Store struct {
	prefix string
	record auth.TokenRecord
}

// TokenByPrefix yields the test's token, and only for its prefix.
func (s *Store) TokenByPrefix(_ context.Context, prefix string) (auth.TokenRecord, error) {
	if prefix != s.prefix {
		return auth.TokenRecord{}, auth.ErrTokenNotFound
	}
	return s.record, nil
}

// TouchToken records the use of the token. No effect here: the freshness of `last_used_at` is not
// what route tests establish.
func (s *Store) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// Token carries a minted token, the auth service that recognises it, and the principal it will
// resolve to.
//
// Plain is the secret in clear, to present in the Authorization header. It exists only in the
// test: the Store keeps nothing but its hash, exactly like the database.
type Token struct {
	Plain string
	Auth  auth.Service

	// Record is the token as the store will yield it. A test can read it to assert on the
	// expected identity, never to bypass it.
	Record auth.TokenRecord
}

// New mints a token whose scope the test chooses, and mounts the auth service that recognises it.
//
// The secret and its hash are built HERE, by the real crypto.NewToken: a test supplying them would
// prove the consistency of its own constants, not that of the authentication.
//
// The record fields the caller left unset are filled in — an identifier if absent, and the use
// timestamp. Everything else is what the test asked for, uncorrected: an inconsistent scope must
// be PRESENTABLE, because that is exactly what the containment tests exist to reject.
func New(t *testing.T, record auth.TokenRecord) Token {
	t.Helper()

	tok, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("authtest: minting the token: %v", err)
	}

	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	record.SecretHash = tok.Hash
	record.LastUsedAt = time.Now()

	return Token{
		Plain:  tok.Plain,
		Auth:   auth.New(&Store{prefix: tok.Prefix, record: record}, nil),
		Record: record,
	}
}

// Admin mints a token of admin scope.
//
// teamID is the team the token CARRIES: uuid.Nil for the global admin, the one the bootstrap
// really creates. An admin carrying a team can no longer be inserted in the database since
// migration 000006, and that is precisely why a test must be able to present one: the defence in
// the code must not rest on a constraint written in another file.
func Admin(t *testing.T, teamID uuid.UUID) Token {
	t.Helper()
	return New(t, auth.TokenRecord{Scope: auth.ScopeAdmin, TeamID: teamID})
}

// Project mints an agent token, scoped to a team and a project.
func Project(t *testing.T, teamID, projectID uuid.UUID) Token {
	t.Helper()
	return New(t, auth.TokenRecord{
		Scope:     auth.ScopeProject,
		TeamID:    teamID,
		ProjectID: projectID,
	})
}

// Authorize sets the Authorization header on a request and yields it, so that the call fits on
// one line at the point of use.
func (tk Token) Authorize(req *http.Request) *http.Request {
	req.Header.Set("Authorization", "Bearer "+tk.Plain)
	return req
}
