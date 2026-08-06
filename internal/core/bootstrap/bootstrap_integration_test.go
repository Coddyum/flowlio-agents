package bootstrap_test

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                      | Résumé                                              | Ligne |
// |------------------------------|------------------------------------------------------|-------|
// | newRealStore                 | Mounts the store on a transaction that is rolled back  | 41    |
// | liveAdminTokens              | Counts the admin tokens the installation would accept  | 72    |
// | TestRotateAdminTokenReplaces | The rotation revokes EVERY live admin token            | 90    |
//
// Fin du sommaire.
// =====================================================================
//
// The revocation is a WHERE clause, so it is proven against Postgres. An in-memory double would
// only replay the predicate, and the defect this guards against is precisely the predicate drifting
// — a rotation that leaves the lost token live looks identical from the outside.
//
// EVERYTHING RUNS IN A TRANSACTION THAT IS ROLLED BACK, and that is not tidiness.
//
// `RevokeAdminTokens` names no identifier: it takes every live admin token of the installation,
// because whoever runs a rotation cannot designate the one they lost. Run against the development
// database — which is what `make test-integration` does — a test of that query touches the
// developer's own admin token. The first version of this file "isolated" itself with a
// `DELETE FROM tokens WHERE scope = 'admin'`, and it destroyed the credential of the instance it
// was running on. A test whose subject is a global write has no business writing outside a
// transaction.

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-agents/internal/database"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newRealStore mounts the bootstrap store on a REAL transaction, rolled back when the test ends.
// The returned *sql.Tx is what the assertions read: the rows never exist outside it.
func newRealStore(t *testing.T) (bootstrap.Store, *sql.Tx) {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL not set — integration test skipped")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("opening the transaction: %v", err)
	}
	t.Cleanup(func() {
		// Rollback, never commit: this test revokes every admin token it can see, including the ones
		// of the instance running it.
		if err := tx.Rollback(); err != nil {
			t.Errorf("rolling back: %v", err)
		}
	})

	return bootstrap.NewStore(database.New(tx)), tx
}

// liveAdminTokens counts the admin tokens the installation would still accept, as seen from inside
// the transaction.
func liveAdminTokens(t *testing.T, tx *sql.Tx) int {
	t.Helper()

	var count int
	if err := tx.QueryRow(
		"SELECT count(*) FROM tokens WHERE scope = 'admin' AND revoked_at IS NULL",
	).Scan(&count); err != nil {
		t.Fatalf("counting the live admin tokens: %v", err)
	}
	return count
}

// TestRotateAdminTokenReplaces is the criterion of FLWL-70 point 6: a way back in that needs no
// access to the database, and that leaves NO live token behind it but the new one.
//
// Both halves are checked. A rotation that issues without revoking would answer the operator while
// leaving the lost credential live on an installation anyone can reach — which is the situation
// being fixed, with one more key in circulation.
func TestRotateAdminTokenReplaces(t *testing.T) {
	st, tx := newRealStore(t)
	ctx := context.Background()

	// The baseline is whatever the installation already holds. Counting it instead of deleting it is
	// what makes the assertion "every live token, not just mine" — and what keeps this test from
	// touching a credential it does not own.
	baseline := liveAdminTokens(t, tx)

	if err := st.CreateAdminToken(ctx, "lost", "aaaaaaaaaaaa", "hash-a"); err != nil {
		t.Fatalf("seeding the lost token: %v", err)
	}
	if err := st.CreateAdminToken(ctx, "also lost", "bbbbbbbbbbbb", "hash-b"); err != nil {
		t.Fatalf("seeding the second token: %v", err)
	}

	token, revoked, err := bootstrap.RotateAdminToken(ctx, st)
	if err != nil {
		t.Fatalf("RotateAdminToken: %v", err)
	}
	if want := int64(baseline + 2); revoked != want {
		t.Errorf("%d token(s) revoked, want %d — the rotation must reach every live one", revoked, want)
	}
	if token == "" {
		t.Fatal("no new token was issued")
	}
	if n := liveAdminTokens(t, tx); n != 1 {
		t.Fatalf("%d live admin token(s) after the rotation, want exactly 1", n)
	}

	// The one that survives is the new one, not either of the seeded pair.
	var prefix string
	if err := tx.QueryRow(
		"SELECT prefix FROM tokens WHERE scope = 'admin' AND revoked_at IS NULL",
	).Scan(&prefix); err != nil {
		t.Fatalf("reading the surviving token: %v", err)
	}
	if prefix == "aaaaaaaaaaaa" || prefix == "bbbbbbbbbbbb" {
		t.Errorf("the surviving token is a seeded one (%s): the rotation revoked the wrong rows", prefix)
	}

	// Rotating again still issues one: the "already bootstrapped" condition belongs to the first run,
	// and reintroducing it in the recovery path would leave a locked-out installation locked out.
	if _, revoked, err = bootstrap.RotateAdminToken(ctx, st); err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if revoked != 1 {
		t.Errorf("%d token(s) revoked on the second rotation, want 1", revoked)
	}
	if n := liveAdminTokens(t, tx); n != 1 {
		t.Errorf("%d live admin token(s) after the second rotation, want 1", n)
	}
}
