package store_test

// WHAT THIS FILE LOCKS DOWN: deleting a repo NEVER costs a sibling repo anything.
//
// The danger is not hypothetical and it is not about status codes. `issues` reaches `projects`
// through TWO foreign keys — `project_id`, the recipient, and `author_project_id`, the author —
// and both are ON DELETE CASCADE. So deleting WEB destroys the questions CORE wrote to WEB, with
// CORE's own words in them, from CORE's side, without CORE asking for anything.
//
// The assertion that matters is therefore a COUNT ON THE SIBLING, never a code returned to the
// caller. A test reading the outcome of the delete would stay green with the guard removed as long
// as somebody kept returning "refused"; a test counting what CORE still holds cannot.
//
// Verified by mutation: dropping `NOT EXISTS (SELECT 1 FROM blockers)` from DeleteProject
// (sql/queries/projects.sql) turns the two counts below from 1 to 0 and the test red, while the
// status-shaped assertions elsewhere in the suite are the ones that report the refusal.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// newProject creates a repo in a team, failing the test rather than returning an error: every
// caller below treats a failure here as a broken fixture, not as a case.
func newProject(t *testing.T, st store.Store, teamID uuid.UUID, key, name string) store.Project {
	t.Helper()

	project, err := st.CreateProject(context.Background(), teamID, key, name)
	if err != nil {
		t.Fatalf("CreateProject %s: %v", key, err)
	}
	return project
}

// openThread has `author` open a question at `recipient`, and has the recipient answer it.
//
// BOTH ENDS SPEAK, and that is not decoration. With only the author's message, the message count of
// the repo playing the recipient is zero in the fixture itself, and the "the words survived"
// assertion would hold on an empty set — passing for a reason that has nothing to do with the
// guarantee. One message per end means that count is never zero for either role.
//
// The insert is raw SQL on purpose: this package's store does not create issues, and routing the
// fixture through the issue feature would make this file depend on a sibling module to state a
// property about the database.
func openThread(t *testing.T, db *sql.DB, teamID uuid.UUID, recipient, author store.Project, number int64, title string) {
	t.Helper()

	var issueID uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state)
		 VALUES ($1, $2, $3, $4, $5, 'answered') RETURNING id`,
		teamID, recipient.ID, author.ID, number, title,
	).Scan(&issueID); err != nil {
		t.Fatalf("opening %s → %s: %v", author.Key, recipient.Key, err)
	}

	for _, speaker := range []store.Project{author, recipient} {
		if _, err := db.Exec(
			`INSERT INTO issue_messages (issue_id, author_project_id, body_md) VALUES ($1, $2, $3)`,
			issueID, speaker.ID, speaker.Key+" speaking.",
		); err != nil {
			t.Fatalf("writing %s's message: %v", speaker.Key, err)
		}
	}
}

// countThreads counts the issues a repo is an end of, in either role.
func countThreads(t *testing.T, db *sql.DB, teamID, projectID uuid.UUID) int {
	t.Helper()

	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM issues
		 WHERE team_id = $1 AND (project_id = $2 OR author_project_id = $2)`,
		teamID, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("counting threads: %v", err)
	}
	return n
}

// countWords counts the messages a repo actually wrote. A thread that survives with the sibling's
// messages gone would be an empty shell, and the count above alone would not notice.
func countWords(t *testing.T, db *sql.DB, projectID uuid.UUID) int {
	t.Helper()

	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM issue_messages WHERE author_project_id = $1`, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("counting messages: %v", err)
	}
	return n
}

// CORE asks WEB a question. Deleting WEB must leave CORE holding exactly what it held.
//
// The two roles are covered in both directions by the sub-tests: the deleted repo is the RECIPIENT
// in the first and the AUTHOR in the second. One cascade per foreign key, and a guard that closed
// only one of them would show here.
func TestDeletingARepoLeavesTheSiblingsThreadsStanding(t *testing.T) {
	cases := []struct {
		name string
		// deletedIsRecipient says which end of the thread is the repo being deleted.
		deletedIsRecipient bool
	}{
		{"the deleted repo is the recipient", true},
		{"the deleted repo is the author", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, db := newStore(t)
			team := createTeam(t, st, db)

			core := newProject(t, st, team.ID, "CORE", "The core")
			web := newProject(t, st, team.ID, "WEB", "The web front")

			if tc.deletedIsRecipient {
				openThread(t, db, team.ID, web, core, 1, "why is the build red?")
			} else {
				openThread(t, db, team.ID, core, web, 1, "why is the build red?")
			}

			// Positive control: without it, a fixture that opened nothing would make every
			// assertion below pass on an empty database.
			threadsBefore := countThreads(t, db, team.ID, core.ID)
			wordsBefore := countWords(t, db, core.ID)
			if threadsBefore != 1 {
				t.Fatalf("CORE holds %d thread(s) before the deletion, want 1 — the fixture opened nothing",
					threadsBefore)
			}
			// The word count has to be non-zero BEFORE, in both roles, or the assertion on it
			// further down would pass on an empty set whatever the deletion did.
			if wordsBefore != 1 {
				t.Fatalf("CORE has written %d message(s) before the deletion, want 1 — the fixture "+
					"left CORE silent, and the assertion on its words would prove nothing", wordsBefore)
			}

			if _, err := st.DeleteProject(context.Background(), team.ID, web.ID); err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}

			threadsAfter := countThreads(t, db, team.ID, core.ID)
			if threadsAfter != threadsBefore {
				t.Errorf("CORE held %d thread(s) before WEB was deleted and holds %d after — deleting "+
					"a repo erased a sibling's questions from the sibling's own side",
					threadsBefore, threadsAfter)
			}

			wordsAfter := countWords(t, db, core.ID)
			if wordsAfter != wordsBefore {
				t.Errorf("CORE had written %d message(s) before WEB was deleted and has %d after — "+
					"the thread survived but the words in it did not",
					wordsBefore, wordsAfter)
			}
		})
	}
}

// The refusal NAMES the sibling and counts its threads, because "no" alone leaves a human with no
// next move: nothing in the product lists the threads of a repo they are trying to retire.
func TestTheRefusalNamesTheSiblingAndItsThreads(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	core := newProject(t, st, team.ID, "CORE", "The core")
	web := newProject(t, st, team.ID, "WEB", "The web front")

	openThread(t, db, team.ID, web, core, 1, "why is the build red?")
	openThread(t, db, team.ID, core, web, 2, "when does the schema change?")

	outcome, err := st.DeleteProject(context.Background(), team.ID, web.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if outcome.Deleted {
		t.Fatal("the store reports WEB deleted while CORE holds two threads with it")
	}
	if len(outcome.Blockers) != 1 {
		t.Fatalf("blockers = %#v, want exactly one — CORE, holding both threads", outcome.Blockers)
	}
	if outcome.Blockers[0].Key != "CORE" {
		t.Errorf("blocker key = %q, want %q", outcome.Blockers[0].Key, "CORE")
	}
	if outcome.Blockers[0].Threads != 2 {
		t.Errorf("blocker threads = %d, want 2 — one in each direction", outcome.Blockers[0].Threads)
	}
}

// A CLOSED thread blocks exactly like an open one: it still carries the sibling's words, and no
// route of this product deletes an issue.
//
// Overruling this is one word in the join of DeleteProject — `AND i.state <> 'closed'` — and this
// test is where that change surfaces.
func TestAClosedThreadStillBlocksTheDeletion(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	core := newProject(t, st, team.ID, "CORE", "The core")
	web := newProject(t, st, team.ID, "WEB", "The web front")

	openThread(t, db, team.ID, web, core, 1, "why is the build red?")
	if _, err := db.Exec(
		`UPDATE issues SET state = 'closed', closed_at = now() WHERE team_id = $1`, team.ID,
	); err != nil {
		t.Fatalf("closing the thread: %v", err)
	}

	outcome, err := st.DeleteProject(context.Background(), team.ID, web.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if outcome.Deleted {
		t.Fatal("WEB was deleted although CORE's closed thread still carries CORE's words")
	}
	if len(outcome.Blockers) != 1 || outcome.Blockers[0].Key != "CORE" {
		t.Errorf("blockers = %#v, want CORE alone", outcome.Blockers)
	}
}

// THE POSITIVE CONTROL. Without it, a guard refusing EVERY deletion would pass for correct, and the
// route would be dead code that nobody could tell from a working one.
func TestARepoNobodyTalksToIsDeleted(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	newProject(t, st, team.ID, "CORE", "The core")
	web := newProject(t, st, team.ID, "WEB", "The web front")

	outcome, err := st.DeleteProject(ctx, team.ID, web.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if !outcome.Deleted {
		t.Fatalf("the store reports WEB not deleted, blockers = %#v — nobody holds a thread with it",
			outcome.Blockers)
	}
	if len(outcome.Blockers) != 0 {
		t.Errorf("blockers = %#v on a deletion that went through", outcome.Blockers)
	}

	// The row is really gone, and so is what hung off it: reading it back is what tells a deletion
	// from a store that merely says one happened.
	if _, err := st.ProjectByID(ctx, team.ID, web.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reading WEB back: error = %v, want ErrNotFound", err)
	}
	var edges int
	if err := db.QueryRow(
		`SELECT count(*) FROM project_trust WHERE team_id = $1`, team.ID,
	).Scan(&edges); err != nil {
		t.Fatalf("counting the edges: %v", err)
	}
	if edges != 0 {
		t.Errorf("%d trust edge(s) survive the deleted repo, want 0", edges)
	}
}

// A repo that is not in the team is NOT FOUND, and that includes a repo that exists next door. The
// refusal is the same one an unknown identifier gets: "it exists but not for you" would let an
// admin pinned to a team probe the installation by sweeping identifiers.
func TestDeletingAProjectIsScopedToItsTeam(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := createTeam(t, st, db)
	teamB := createTeam(t, st, db)
	web := newProject(t, st, teamA.ID, "WEB", "A's web front")

	if _, err := st.DeleteProject(ctx, teamB.ID, web.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("team B deleting A's repo: error = %v, want ErrNotFound", err)
	}
	if _, err := st.ProjectByID(ctx, teamA.ID, web.ID); err != nil {
		t.Errorf("A's repo did not survive B's attempt: %v", err)
	}

	if _, err := st.DeleteProject(ctx, teamA.ID, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown identifier: error = %v, want ErrNotFound", err)
	}
}
