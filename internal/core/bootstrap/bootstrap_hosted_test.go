package bootstrap

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                          | Résumé                                          | Ligne |
// |----------------------------------|--------------------------------------------------|-------|
// | fakeHostedStore                  | A hosted store whose one row is written by hand    | 41    |
// | fakeHostedStore.AdminTokenByPrefix| Yields the configured row, or ErrNoSuchPrefix     | 52    |
// | fakeHostedStore.CreateAdminToken | Records the insert instead of performing it        | 61    |
// | TestEnsureHostedAdminToken       | Every outcome the start-up has to tell apart       | 72    |
// | TestEnsureHostedAdminTokenNeverEchoesTheSecret | No refusal repeats what it refused   | 163   |
//
// Fin du sommaire.
// =====================================================================
//
// The subject is a START-UP GUARD, so what matters is not that it usually works but that each way
// of being misconfigured is told apart from the others. A hosted instance whose administration
// token is revoked answers 401 to its own operator, exactly like one whose token was never
// registered — and the two are repaired by different gestures. Collapsing them into one error, or
// into a silent success, is the defect this file exists to catch.
//
// An in-memory double is the right instrument here, unlike the rotation's integration test next
// door: nothing under test is a WHERE clause. The judgement lives entirely in Go, and the store is
// reduced to "what does this prefix point at".

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

// createdToken is what an insert would have written.
type createdToken struct {
	name, prefix, hash string
}

// fakeHostedStore holds at most one registered token, written by hand by the case at hand.
type fakeHostedStore struct {
	// row is what the prefix points at; found says whether it points at anything.
	row   StoredToken
	found bool

	created *createdToken
	inserts int
}

// AdminTokenByPrefix yields the configured row, or ErrNoSuchPrefix when there is none.
func (f *fakeHostedStore) AdminTokenByPrefix(context.Context, string) (StoredToken, error) {
	if !f.found {
		return StoredToken{}, ErrNoSuchPrefix
	}
	return f.row, nil
}

// CreateAdminToken records the insert instead of performing it.
func (f *fakeHostedStore) CreateAdminToken(_ context.Context, name, prefix, hash string) error {
	f.created = &createdToken{name: name, prefix: prefix, hash: hash}
	f.inserts++
	return nil
}

// TestEnsureHostedAdminToken drives every outcome the start-up has to tell apart.
//
// `wantInserts` is asserted in EVERY case, refusals included. Without it a version that inserted a
// second row before noticing the token was revoked would still be "an error", and the table would
// stay green on a start-up that quietly duplicates a credential.
func TestEnsureHostedAdminToken(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("crypto.NewToken: %v", err)
	}
	other, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("crypto.NewToken: %v", err)
	}

	cases := []struct {
		name        string
		plain       string
		store       fakeHostedStore
		wantErr     string
		wantInserts int
	}{
		{
			name:        "unknown prefix is registered",
			plain:       token.Plain,
			wantInserts: 1,
		},
		{
			name:  "the same token twice is a silent success",
			plain: token.Plain,
			store: fakeHostedStore{
				found: true,
				row:   StoredToken{Scope: adminScope, SecretHash: token.Hash},
			},
		},
		{
			name:  "a revoked token is refused, and named as revoked",
			plain: token.Plain,
			store: fakeHostedStore{
				found: true,
				row:   StoredToken{Scope: adminScope, SecretHash: token.Hash, Revoked: true},
			},
			wantErr: "revoked",
		},
		{
			name:  "a prefix held by a project token is refused",
			plain: token.Plain,
			store: fakeHostedStore{
				found: true,
				row:   StoredToken{Scope: "project", SecretHash: token.Hash},
			},
			wantErr: "not an administration one",
		},
		{
			name:  "the same prefix with another secret is refused",
			plain: token.Plain,
			store: fakeHostedStore{
				found: true,
				row:   StoredToken{Scope: adminScope, SecretHash: other.Hash},
			},
			wantErr: "different secret",
		},
		{
			name:    "a token that is not a flowlio token is refused before any lookup",
			plain:   "not-a-token",
			wantErr: "not a flowlio token",
		},
		{
			name:    "an empty token is refused",
			plain:   "",
			wantErr: "not a flowlio token",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := c.store
			err := EnsureHostedAdminToken(context.Background(), &st, c.plain)

			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("EnsureHostedAdminToken = %v, want nil", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("EnsureHostedAdminToken = nil, want an error naming %q", c.wantErr)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Fatalf("EnsureHostedAdminToken = %q, want it to name %q", err, c.wantErr)
			}

			if st.inserts != c.wantInserts {
				t.Fatalf("%d insert(s), want %d", st.inserts, c.wantInserts)
			}
			if c.wantInserts == 0 {
				return
			}

			prefix, secret, perr := crypto.ParseToken(c.plain)
			if perr != nil {
				t.Fatalf("ParseToken: %v", perr)
			}
			if st.created.prefix != prefix {
				t.Errorf("registered prefix %q, want %q", st.created.prefix, prefix)
			}
			// The HASH, not the secret: what lands in the database must never be the thing the
			// environment carried.
			if st.created.hash != crypto.HashSecret(secret) {
				t.Errorf("registered hash %q, want the hash of the configured secret", st.created.hash)
			}
			if st.created.hash == secret {
				t.Error("the secret itself was stored")
			}
			if st.created.name != hostedAdminTokenName {
				t.Errorf("registered name %q, want %q", st.created.name, hostedAdminTokenName)
			}
		})
	}
}

// TestEnsureHostedAdminTokenNeverEchoesTheSecret keeps a live credential out of the deploy log.
//
// Every refusal here is printed by main.go through log.Fatalf, straight into the platform's log —
// durable, and readable by anyone who can see the deployment. An error message that quoted the
// token it was refusing would publish it there, and the token is refused, not invalid: a revoked
// one is still the operator's, and a wrong-secret one still belongs to some other instance.
func TestEnsureHostedAdminTokenNeverEchoesTheSecret(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("crypto.NewToken: %v", err)
	}
	_, secret, err := crypto.ParseToken(token.Plain)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}

	stores := map[string]fakeHostedStore{
		"revoked":      {found: true, row: StoredToken{Scope: adminScope, SecretHash: token.Hash, Revoked: true}},
		"wrong scope":  {found: true, row: StoredToken{Scope: "project", SecretHash: token.Hash}},
		"wrong secret": {found: true, row: StoredToken{Scope: adminScope, SecretHash: "0"}},
	}

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			st := store
			err := EnsureHostedAdminToken(context.Background(), &st, token.Plain)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), token.Plain) {
				t.Errorf("the refusal repeats the secret: %q", err)
			}
		})
	}
}

// ensure the fake keeps satisfying the contract the production store implements.
var _ HostedStore = (*fakeHostedStore)(nil)

// ensure ErrNoSuchPrefix stays comparable with errors.Is, which the production path relies on.
var _ = errors.Is(ErrNoSuchPrefix, ErrNoSuchPrefix)
