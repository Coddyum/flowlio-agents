package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newStore opens the test database. Without FLOWLIO_TEST_DATABASE_URL the test is skipped: the unit
// suite has to stay runnable with no infrastructure.
func newStore(t *testing.T) (store.Store, *sql.DB) {
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

	return store.New(database.New(db)), db
}

// createTeam creates a throwaway team and schedules its deletion: the related rows go in cascade,
// so the test database does not drift from one run to the next.
func createTeam(t *testing.T, st store.Store, db *sql.DB) store.Team {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	team, err := st.CreateTeam(context.Background(), slug, "Test team")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", team.ID); err != nil {
			t.Errorf("cleaning up team %s: %v", team.ID, err)
		}
	})
	return team
}

func TestTeamLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	bySlug, err := st.TeamBySlug(ctx, team.Slug)
	if err != nil {
		t.Fatalf("TeamBySlug: %v", err)
	}
	if bySlug.ID != team.ID {
		t.Errorf("TeamBySlug returns %s, want %s", bySlug.ID, team.ID)
	}

	byID, err := st.TeamByID(ctx, team.ID)
	if err != nil {
		t.Fatalf("TeamByID: %v", err)
	}
	if byID.Slug != team.Slug {
		t.Errorf("TeamByID returns %s, want %s", byID.Slug, team.Slug)
	}

	if _, err := st.CreateTeam(ctx, team.Slug, "Duplicate"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate slug: error = %v, want ErrConflict", err)
	}

	if _, err := st.TeamBySlug(ctx, "missing-slug-"+uuid.NewString()[:8]); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown slug: error = %v, want ErrNotFound", err)
	}
}

// Central security property: a project key belonging to another team is not found, not merely
// forbidden.
func TestProjectsAreIsolatedAcrossTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := createTeam(t, st, db)
	teamB := createTeam(t, st, db)

	projectA, err := st.CreateProject(ctx, teamA.ID, "CORE", "A's core")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := st.ProjectByKey(ctx, teamB.ID, "CORE"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("team B sees A's project: error = %v, want ErrNotFound", err)
	}
	if _, err := st.ProjectByID(ctx, teamB.ID, projectA.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("team B reads A's project by identifier: error = %v, want ErrNotFound", err)
	}

	projects, err := st.ListProjects(ctx, teamB.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("team B lists %d projects, want 0", len(projects))
	}

	// The same key stays available in another team: uniqueness is per team, not global.
	if _, err := st.CreateProject(ctx, teamB.ID, "CORE", "B's core"); err != nil {
		t.Errorf("key CORE refused in team B: %v", err)
	}
	if _, err := st.CreateProject(ctx, teamA.ID, "CORE", "duplicate in A"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate key in team A: error = %v, want ErrConflict", err)
	}
}

func TestTokenLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	team := createTeam(t, st, db)
	other := createTeam(t, st, db)

	project, err := st.CreateProject(ctx, team.ID, "CORE", "core")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	token, err := st.CreateToken(ctx, team.ID, project.ID, "agent", prefix, "test-hash")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	tokens, err := st.ListTokens(ctx, team.ID, project.ID)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != token.ID {
		t.Fatalf("ListTokens returns %d tokens, want the one just created", len(tokens))
	}

	// Another team cannot revoke this token.
	if _, err := st.RevokeToken(ctx, other.ID, token.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross revocation: error = %v, want ErrNotFound", err)
	}

	revoked, err := st.RevokeToken(ctx, team.ID, token.ID)
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if !revoked.Revoked {
		t.Error("a revoked token must be marked as such")
	}

	if _, err := st.RevokeToken(ctx, team.ID, token.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second revocation: error = %v, want ErrNotFound", err)
	}
}

// An admin token carries NEITHER team NOR project, and the database refuses it — not just the code.
//
// The shape `scope='admin' AND team_id IS NOT NULL` was legal until migration 000006: nothing
// produced it, so it was invisible, and it armed a trap for the first session with a reason to
// create an "admin pinned to a team". The repo's doctrine is to make the illegal shape NOT
// INSERTABLE rather than merely not produced.
//
// The SQL is written here directly, without going through the store: the store exposes no path for
// creating that row, and that is precisely what we do not want to have to take on faith.
//
// MUTATION: restoring the 000002 constraint (`scope='admin' AND project_id IS NULL`, without the
// clause on team_id) makes the first case pass, hence this test fail.
func TestDatabaseRejectsAdminTokenCarryingATeam(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	project, err := st.CreateProject(context.Background(), team.ID, "CORE", "core")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	forbidden := []struct {
		name      string
		teamID    any
		projectID any
	}{
		{"admin with a team", team.ID, nil},
		{"admin with a project", nil, project.ID},
		{"fully scoped admin", team.ID, project.ID},
	}

	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
			_, err := db.Exec(
				`INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
				 VALUES ('admin', $1, $2, 'trap', $3, 'test-hash')`,
				tc.teamID, tc.projectID, prefix,
			)
			if err == nil {
				// The row should never exist: we remove it so as not to pollute the dev database,
				// then we fail.
				_, _ = db.Exec("DELETE FROM tokens WHERE prefix = $1", prefix)
				t.Fatalf("the database accepted an admin token (%s): the tokens_scope_shape constraint does not bound that shape", tc.name)
			}
			if !strings.Contains(err.Error(), "tokens_scope_shape") {
				t.Errorf("refused by something other than tokens_scope_shape: %v", err)
			}
		})
	}

	// Counter-check: the legal shape does go through. Without it, a constraint refusing EVERY admin
	// token would pass for correct.
	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if _, err := db.Exec(
		`INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
		 VALUES ('admin', NULL, NULL, 'global admin', $1, 'test-hash')`, prefix,
	); err != nil {
		t.Fatalf("the database refuses the global admin, which is the only shape bootstrapping produces: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM tokens WHERE prefix = $1", prefix); err != nil {
			t.Errorf("cleaning up token %s: %v", prefix, err)
		}
	})
}

// The key format constraint is carried by the database: the application-level validation gives the
// message, the database gives the guarantee.
func TestDatabaseRejectsMalformedKey(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	for _, key := range []string{"lowercase", "C", "WAY-TOO-LONG-KEY", ""} {
		t.Run(fmt.Sprintf("key %q", key), func(t *testing.T) {
			if _, err := st.CreateProject(context.Background(), team.ID, key, "x"); !errors.Is(err, store.ErrConflict) {
				t.Errorf("error = %v, want ErrConflict (constraint violation)", err)
			}
		})
	}
}

// allowTrust lays down ONE DIRECTED trust edge through direct SQL.
//
// It no longer normalises anything, and it no longer can: since migration 000013 the two columns
// are `from` and `to`, and an `ordered()` helper sorting them — which is what this file used to
// carry — would have made every caller below insert the same row whichever way it named the two
// projects, hiding the direction the table exists to hold.
func allowTrust(t *testing.T, db *sql.DB, teamID, from, to uuid.UUID) error {
	t.Helper()

	_, err := db.Exec(
		"INSERT INTO project_trust (team_id, from_project_id, to_project_id) VALUES ($1, $2, $3)",
		teamID, from, to,
	)
	return err
}

// The database refuses the seven illegal shapes of the trust graph — not the code, the DATABASE.
//
// This is the repo's doctrine applied to `project_trust`: the illegal shape is made NOT INSERTABLE
// rather than merely not produced. The COMPOSITE FKs `(project_id, team_id)` are what does the
// work: the single `team_id` column has to satisfy BOTH at once, so an edge between two projects of
// different teams is impossible, including when the caller LIES about `team_id` — both directions
// of the lie are tested.
//
// WHAT MIGRATION 000013 CHANGED HERE. `project_trust_ordered` used to close two shapes at once, the
// self-edge and the mirror. The mirror is now LEGAL — it is the opposite direction, and the whole
// point of the card — so only the self-edge is still refused, by the CHECK that replaced it,
// project_trust_not_self. The mirror gets a POSITIVE control instead: a test suite that dropped the
// case rather than inverting it would leave nothing observing that the model really is directed at
// the database level.
//
// MUTATION: removing `project_trust_not_self` lets the self-edge through; removing either composite
// FK lets the corresponding cross-team edge through.
func TestDatabaseRejectsIllegalTrustEdges(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := createTeam(t, st, db)
	teamB := createTeam(t, st, db)
	teamC := createTeam(t, st, db)

	core, err := st.CreateProject(ctx, teamA.ID, "CORE", "A's core")
	if err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	front, err := st.CreateProject(ctx, teamA.ID, "FRNT", "A's front")
	if err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}
	neighbour, err := st.CreateProject(ctx, teamB.ID, "CORE", "B's core")
	if err != nil {
		t.Fatalf("CreateProject neighbour: %v", err)
	}

	forbidden := []struct {
		name     string
		exec     func() error
		contains string
	}{
		{
			"cross-team edge, team_id of the sender",
			func() error { return allowTrust(t, db, teamA.ID, core.ID, neighbour.ID) },
			"project_trust_",
		},
		{
			"cross-team edge, lying about team_id",
			func() error { return allowTrust(t, db, teamB.ID, core.ID, neighbour.ID) },
			"project_trust_",
		},
		{
			"cross-team edge, team_id of a third team",
			func() error { return allowTrust(t, db, teamC.ID, core.ID, neighbour.ID) },
			"project_trust_",
		},
		{
			// The self-edge is the one shape 000013 had to close again by hand: the ordering CHECK
			// used to exclude equality for free, and it is gone.
			"self-edge",
			func() error { return allowTrust(t, db, teamA.ID, core.ID, core.ID) },
			"project_trust_not_self",
		},
	}

	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.exec()
			if err == nil {
				t.Fatalf("the database accepted: %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("refused by something other than %s: %v", tc.contains, err)
			}
		})
	}

	// The legal edges are PRESENT, BOTH OF THEM — without a positive control, a constraint refusing
	// EVERYTHING would pass for correct.
	//
	// They are no longer laid down here. Creating FRNT in teamA already opened CORE→FRNT and
	// FRNT→CORE, in the same statement as the project (card 12 doubled by card 11,
	// sql/queries/projects.sql): re-inserting either would now hit project_trust_pkey, and the
	// assertion would read as "the constraints refuse a legal edge". Reading the rows proves the
	// same thing — the shape passed every constraint — through the path that actually writes it.
	var legal int
	if err := db.QueryRow(
		`SELECT count(*) FROM project_trust
		 WHERE team_id = $1 AND (from_project_id, to_project_id) IN (($2, $3), ($3, $2))`,
		teamA.ID, core.ID, front.ID,
	).Scan(&legal); err != nil {
		t.Fatalf("reading the legal edges: %v", err)
	}
	if legal != 2 {
		t.Fatalf("CORE and FRNT hold %d edges, want 2 (one per direction): either the constraints "+
			"refuse a legal edge, or creating a repo no longer links it to its peers both ways", legal)
	}

	t.Run("duplicate in the same direction", func(t *testing.T) {
		if err := allowTrust(t, db, teamA.ID, core.ID, front.ID); err == nil {
			t.Fatal("the database accepted a duplicate (CORE→FRNT is already declared)")
		} else if !strings.Contains(err.Error(), "project_trust_pkey") {
			t.Errorf("refused by something other than the primary key: %v", err)
		}
	})

	// THE POSITIVE CONTROL THAT REPLACES THE OLD "non-canonical pair (mirror)" CASE. Under 000007
	// that insert was refused by project_trust_ordered; under 000013 it is a different row, and it
	// must be accepted. Nothing else in the suite reads that fact off the table itself, so a
	// migration that forgot to drop the ordering CHECK would surface here and nowhere else.
	t.Run("the mirror is a different row, not a duplicate", func(t *testing.T) {
		if _, err := db.Exec(
			"DELETE FROM project_trust WHERE team_id = $1 AND from_project_id = $2 AND to_project_id = $3",
			teamA.ID, front.ID, core.ID,
		); err != nil {
			t.Fatalf("removing FRNT→CORE: %v", err)
		}
		if err := allowTrust(t, db, teamA.ID, front.ID, core.ID); err != nil {
			t.Fatalf("FRNT→CORE refused while CORE→FRNT exists: %v — the edge is still a pair", err)
		}
	})

	t.Run("moving a project to another team", func(t *testing.T) {
		if _, err := db.Exec("UPDATE projects SET team_id = $1 WHERE id = $2", teamB.ID, core.ID); err == nil {
			t.Error("a project carrying an edge was able to change team")
		}
	})

	t.Run("moving an edge to another team", func(t *testing.T) {
		if _, err := db.Exec("UPDATE project_trust SET team_id = $1 WHERE team_id = $2", teamB.ID, teamA.ID); err == nil {
			t.Error("an edge was able to be moved into another team")
		}
	})

	t.Run("deleting a project: cascade", func(t *testing.T) {
		if _, err := db.Exec("DELETE FROM projects WHERE id = $1", front.ID); err != nil {
			t.Fatalf("deleting the project: %v", err)
		}
		var remaining int
		if err := db.QueryRow(
			"SELECT count(*) FROM project_trust WHERE team_id = $1", teamA.ID,
		).Scan(&remaining); err != nil {
			t.Fatalf("counting: %v", err)
		}
		if remaining != 0 {
			t.Errorf("%d edge(s) survive the deleted project, want 0", remaining)
		}
	})
}
