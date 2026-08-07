package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                                    | Résumé                                | Ligne |
// |--------------------------------------------|----------------------------------------|-------|
// | TestMintAdminTokenPrintsTheTokenAndNothingElse | The output is one line, and it is a usable token | 34 |
// | TestMintAdminTokenNeverRepeats             | Two runs never yield the same secret    | 64    |
// | fakeMintStore                              | A hosted store on a database holding nothing | 81 |
// | fakeMintStore.AdminTokenByPrefix           | Always reports an unregistered prefix   | 84    |
// | fakeMintStore.CreateAdminToken             | Counts the insert instead of doing it   | 89    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

// TestMintAdminTokenPrintsTheTokenAndNothingElse pins the exact shape of the output, because the
// command is meant to be captured: `ADMIN_TOKEN=$(flowlio-api mint-admin-token)`. A banner, a blank
// line or a closing hint would be captured with it, and the instance would then register a token
// nobody can present. "The output contains a token" would pass on every one of those.
//
// The second half is what makes the file worth having: what comes out has to be a token this very
// binary accepts. Generation and verification are two functions, and a format drifting between them
// would surface as an instance refusing its own freshly minted credential — at start-up, during a
// deploy, which is the worst place to find out.
func TestMintAdminTokenPrintsTheTokenAndNothingElse(t *testing.T) {
	var out strings.Builder
	if err := mintAdminToken(&out); err != nil {
		t.Fatalf("mintAdminToken: %v", err)
	}

	printed := out.String()
	if !strings.HasSuffix(printed, "\n") || strings.Count(printed, "\n") != 1 {
		t.Fatalf("output %q, want exactly one line", printed)
	}

	token := strings.TrimSuffix(printed, "\n")
	if _, _, err := crypto.ParseToken(token); err != nil {
		t.Fatalf("the minted token is not parseable: %v", err)
	}

	// And the start-up guard it will meet next accepts it, on a database holding nothing — the
	// exact situation of a first hosted deploy.
	st := &fakeMintStore{}
	if err := bootstrap.EnsureHostedAdminToken(context.Background(), st, token); err != nil {
		t.Fatalf("the hosted bootstrap refused a freshly minted token: %v", err)
	}
	if st.inserts != 1 {
		t.Errorf("%d registration(s), want 1", st.inserts)
	}
}

// TestMintAdminTokenNeverRepeats guards the one property a shared secret cannot lose. A generator
// seeded once — or one that fell back to a constant on an unreadable entropy source — would hand
// every hosted instance the same administration credential.
func TestMintAdminTokenNeverRepeats(t *testing.T) {
	const runs = 64

	seen := make(map[string]bool, runs)
	for range runs {
		var out strings.Builder
		if err := mintAdminToken(&out); err != nil {
			t.Fatalf("mintAdminToken: %v", err)
		}
		if seen[out.String()] {
			t.Fatal("mint-admin-token produced the same token twice")
		}
		seen[out.String()] = true
	}
}

// fakeMintStore is a hosted store on a database that holds nothing.
type fakeMintStore struct{ inserts int }

// AdminTokenByPrefix always reports a prefix nothing is registered under.
func (f *fakeMintStore) AdminTokenByPrefix(context.Context, string) (bootstrap.StoredToken, error) {
	return bootstrap.StoredToken{}, bootstrap.ErrNoSuchPrefix
}

// CreateAdminToken counts the insert instead of performing it.
func (f *fakeMintStore) CreateAdminToken(_ context.Context, _, _, _ string) error {
	f.inserts++
	return nil
}
