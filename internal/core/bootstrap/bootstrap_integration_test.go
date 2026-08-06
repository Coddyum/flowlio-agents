package bootstrap_test

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                        | Résumé                                            | Ligne |
// |--------------------------------|----------------------------------------------------|-------|
// | newRealStore                   | Mounts the bootstrap store on the real database     | 30    |
// | liveAdminTokens                | Counts the admin tokens Postgres still accepts      | 51    |
// | TestRotateAdminTokenReplaces   | The rotation revokes every live admin token         | 69    |
//
// Fin du sommaire.
// =====================================================================
//
// The revocation is a WHERE clause, so it is proven against Postgres. An in-memory double would
// only replay the predicate, and the defect this guards against is precisely the predicate drifting
// — a rotation that leaves the lost token live looks identical from the outside.

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-agents/internal/database"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newRealStore mounts the bootstrap store on the real database.
func newRealStore(t *testing.T) (bootstrap.Store, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL not set — integration test skipped")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("database unreachable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return bootstrap.NewStore(database.New(db)), db
}

// liveAdminTokens counts the admin tokens the installation would still accept.
func liveAdminTokens(t *testing.T, db *sql.DB) int {
	t.Helper()

	var count int
	if err := db.QueryRow(
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
	st, db := newRealStore(t)
	ctx := context.Background()

	// The table is shared with whatever else the suite left behind: this test owns the admin tokens
	// it finds, so it starts from a state it wrote itself.
	if _, err := db.Exec("DELETE FROM tokens WHERE scope = 'admin'"); err != nil {
		t.Fatalf("clearing the admin tokens: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM tokens WHERE scope = 'admin'"); err != nil {
			t.Errorf("cleaning up the admin tokens: %v", err)
		}
	})

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
	if revoked != 2 {
		t.Errorf("%d token(s) revoked, want 2", revoked)
	}
	if token == "" {
		t.Fatal("no new token was issued")
	}
	if n := liveAdminTokens(t, db); n != 1 {
		t.Fatalf("%d live admin token(s) after the rotation, want exactly 1", n)
	}

	// The one that survives is the new one, not either of the seeded pair.
	var prefix string
	if err := db.QueryRow(
		"SELECT prefix FROM tokens WHERE scope = 'admin' AND revoked_at IS NULL",
	).Scan(&prefix); err != nil {
		t.Fatalf("reading the surviving token: %v", err)
	}
	if prefix == "aaaaaaaaaaaa" || prefix == "bbbbbbbbbbbb" {
		t.Errorf("the surviving token is a seeded one (%s): the rotation revoked the wrong rows", prefix)
	}

	// Rotating again on an installation whose admin tokens are all revoked still issues one: the
	// "already bootstrapped" condition belongs to the first run, and reintroducing it here would
	// leave a locked-out installation locked out.
	if _, revoked, err = bootstrap.RotateAdminToken(ctx, st); err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if revoked != 1 {
		t.Errorf("%d token(s) revoked on the second rotation, want 1", revoked)
	}
	if n := liveAdminTokens(t, db); n != 1 {
		t.Errorf("%d live admin token(s) after the second rotation, want 1", n)
	}
}
