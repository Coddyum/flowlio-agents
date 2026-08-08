package store_test

// WHAT THIS FILE LOCKS DOWN: deleting a team REALLY takes everything inside it away, and takes
// nothing from the team next door.
//
// THE ASSERTION IS A COUNT, NOT A STATUS. `DeleteTeam` returning nil says only that a statement ran
// without complaining. What the customer bought when they pressed the button is that their repos,
// their threads, the words in those threads and the credentials pinned to them stop existing — and
// the only thing that can say so is counting the rows afterwards. A test reading the store's error
// would stay green against a query that deletes nothing at all.
//
// Verified by mutation: turning `DELETE FROM teams WHERE id = $1 RETURNING id` into
// `SELECT id FROM teams WHERE id = $1` (sql/queries/teams.sql) leaves DeleteTeam answering nil, the
// handler answering 204, and every count below unchanged — this file is what goes red.
//
// THE NEIGHBOUR IS HALF THE TEST. A `DELETE FROM teams` with a forgotten WHERE would satisfy every
// "it is gone" assertion ever written; only a second team, populated the same way and still standing
// afterwards, can tell that apart.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// inhabited is a team populated the way a real one is: two repos, a thread in each direction with a
// message from each end, and a token pinned to one of the repos.
type inhabited struct {
	team store.Team
	core store.Project
	web  store.Project
}

// populate builds that team. The thread is opened in BOTH directions on purpose: `issues` reaches
// `projects` through two different foreign keys, and a fixture using only one of them would leave
// the other cascade untested.
func populate(t *testing.T, st store.Store, db *sql.DB) inhabited {
	t.Helper()

	team := createTeam(t, st, db)
	core := newProject(t, st, team.ID, "CORE", "The core")
	web := newProject(t, st, team.ID, "WEB", "The web front")

	openThread(t, db, team.ID, web, core, 1, "why is the build red?")
	openThread(t, db, team.ID, core, web, 2, "when does the schema change?")

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if _, err := st.CreateToken(context.Background(), team.ID, web.ID, "agent", prefix, "hash"); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	return inhabited{team: team, core: core, web: web}
}

// census is what a team holds, counted from the tables themselves rather than from any store method.
type census struct {
	projects int
	issues   int
	messages int
	tokens   int
}

// take counts a team's rows. Reading `issue_messages` through a join rather than by `team_id` is
// deliberate: that table carries no team, so the count follows the same path the cascade does.
func take(t *testing.T, db *sql.DB, teamID uuid.UUID) census {
	t.Helper()

	var c census
	row := db.QueryRow(`
		SELECT
			(SELECT count(*) FROM projects WHERE team_id = $1),
			(SELECT count(*) FROM issues   WHERE team_id = $1),
			(SELECT count(*) FROM issue_messages m
			   JOIN issues i ON i.id = m.issue_id WHERE i.team_id = $1),
			(SELECT count(*) FROM tokens   WHERE team_id = $1)`, teamID)
	if err := row.Scan(&c.projects, &c.issues, &c.messages, &c.tokens); err != nil {
		t.Fatalf("counting the rows of team %s: %v", teamID, err)
	}
	return c
}

// DELETING A TEAM EMPTIES IT COMPLETELY, and leaves the neighbour exactly as it was.
//
// Every number below is checked BEFORE as well as after. Without the before, a fixture that failed
// to write anything would make "it is all gone" true for a reason that has nothing to do with the
// deletion — which is the way this shape of test usually rots.
func TestDeletingATeamTakesItsReposAndTheirThreads(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	doomed := populate(t, st, db)
	neighbour := populate(t, st, db)

	want := census{projects: 2, issues: 2, messages: 4, tokens: 1}
	if before := take(t, db, doomed.team.ID); before != want {
		t.Fatalf("the doomed team holds %+v before the deletion, want %+v — the fixture wrote nothing "+
			"to delete, and every assertion below would pass on an empty team", before, want)
	}
	if before := take(t, db, neighbour.team.ID); before != want {
		t.Fatalf("the neighbour holds %+v before the deletion, want %+v", before, want)
	}

	if err := st.DeleteTeam(ctx, doomed.team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}

	if after := take(t, db, doomed.team.ID); after != (census{}) {
		t.Errorf("the deleted team still holds %+v, want everything at zero — the customer was told "+
			"their project was gone while its repos, threads, words and tokens are still there", after)
	}

	// The team row itself, read back through the store: a cascade that emptied the team while
	// leaving the team standing would satisfy the count above.
	if _, err := st.TeamByID(ctx, doomed.team.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reading the deleted team back: error = %v, want ErrNotFound", err)
	}

	if after := take(t, db, neighbour.team.ID); after != want {
		t.Errorf("the neighbour holds %+v after the deletion, want %+v — deleting one customer's "+
			"project took another customer's work with it", after, want)
	}
	if _, err := st.ProjectByID(ctx, neighbour.team.ID, neighbour.web.ID); err != nil {
		t.Errorf("the neighbour's WEB did not survive: %v", err)
	}
}

// A CROSS-REPO THREAD IS NO OBSTACLE, and that is the difference with DeleteProject.
//
// Deleting one repo is refused while a sibling holds a thread with it, because the sibling outlives
// the deletion and would lose its own words from its own side. Here both ends go, so the refusal has
// nobody left to protect. This test states that in the only way that means anything: the fixture
// contains exactly the situation `DeleteProject` refuses, and the deletion goes through.
//
// MUTATION: give DeleteTeam the `NOT EXISTS (SELECT 1 FROM blockers)` guard of DeleteProject — this
// test goes red with the team still standing, and it is the only one that would notice.
func TestATeamIsDeletedEvenWhenItsReposTalkToEachOther(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	team := populate(t, st, db)

	// The control: this very pair of repos is undeletable one at a time.
	outcome, err := st.DeleteProject(ctx, team.team.ID, team.web.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if outcome.Deleted {
		t.Fatal("WEB was deletable on its own — the fixture no longer holds the threads this test " +
			"exists to delete through")
	}

	if err := st.DeleteTeam(ctx, team.team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if after := take(t, db, team.team.ID); after != (census{}) {
		t.Errorf("the team still holds %+v after its deletion, want everything at zero", after)
	}
}

// A team that is not there is ErrNotFound, never a silent success.
//
// This is what stops the route answering 204 on an identifier that names nothing: the delete
// reports the row it removed, and no row means no deletion. MUTATION: change the query to `:exec`
// and drop the RETURNING — this test goes red while every other one in the file stays green.
func TestDeletingATeamThatIsNotThereIsNotFound(t *testing.T) {
	st, _ := newStore(t)

	if err := st.DeleteTeam(context.Background(), uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
