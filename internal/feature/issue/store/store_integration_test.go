package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// project is a test project and its tenancy scope: exactly what a token carries.
type project struct {
	teamID uuid.UUID
	id     uuid.UUID
	key    string
}

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

	return store.New(database.New(db), db, cache.NewMemory(time.Hour, time.Hour)), db
}

// newTeam creates a throwaway team. Deleting it takes everything with it in cascade.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Test team",
	).Scan(&teamID); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", teamID, err)
		}
	})
	return teamID
}

// newProject creates a project in a team. The fixtures go through direct SQL: the issue feature
// must depend on no other feature, not even in its tests.
func newProject(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) project {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Project "+key,
	).Scan(&id); err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}
	return project{teamID: teamID, id: id, key: key}
}

// trust declares ONE DIRECTED trust: `from` may open a question at `to`, and nothing more. The
// opposite direction is a second call, and the tests below make it only where they mean it.
//
// The graph is laid down BY HAND in every test that needs it, exactly like the tenancy scope:
// hiding it inside newProject would mask the very guarantee these tests exist to prove. It is the
// only occurrence of `project_trust` in this repo's Go outside generated code, and it sits in a
// test file — a fixture, never a decision.
//
// The signature reads `trust(t, db, from, to)` and the helper does NOT normalise the two ends. A
// fixture that sorted them — which is what this one did while the edge was a pair — would open both
// directions on every call, and every "the other way round is refused" assertion in this package
// would pass on a graph that authorised it.
func trust(t *testing.T, db *sql.DB, from, to project) {
	t.Helper()

	if from.teamID != to.teamID {
		t.Fatalf("trust %s → %s: different teams, the edge is not insertable", from.key, to.key)
	}
	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, from_project_id, to_project_id) VALUES ($1, $2, $3)`,
		from.teamID, from.id, to.id,
	); err != nil {
		t.Fatalf("trust %s → %s: %v", from.key, to.key, err)
	}
}

// open opens an issue from `from` towards `to`, through the nominal path.
func open(t *testing.T, st store.Store, from, to project, title string) store.Issue {
	t.Helper()

	ctx := context.Background()
	var created store.Issue
	err := st.WithTx(ctx, func(tx store.Store) error {
		var err error
		created, err = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          from.teamID,
			AuthorProjectID: from.id,
			ToProjectKey:    to.key,
			Title:           title,
		})
		if err != nil {
			return err
		}
		if err := tx.AddFirstMessage(ctx, created.ID, from.id, "body of the question"); err != nil {
			return err
		}
		return tx.AppendEvent(ctx, store.Event{
			TeamID:         from.teamID,
			ProjectID:      created.ProjectID,
			ActorProjectID: from.id,
			Kind:           store.KindIssueOpened,
			SubjectID:      created.ID,
		})
	})
	if err != nil {
		t.Fatalf("opening issue %q: %v", title, err)
	}
	return created
}

// refFor composes an issue's reference for a given caller.
func refFor(caller project, target project, number int64) store.Ref {
	return store.Ref{
		TeamID:          caller.teamID,
		CallerProjectID: caller.id,
		ProjectKey:      target.key,
		Number:          number,
	}
}

func TestIssueConversation(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, web, core)

	issue := open(t, st, web, core, "The endpoint returns 500 on an empty slug")
	if issue.Number != 1 {
		t.Errorf("number = %d, want 1 (the RECIPIENT's counter)", issue.Number)
	}
	if issue.State != "open" {
		t.Errorf("state = %q, want open", issue.State)
	}

	ref := refFor(core, core, issue.Number)

	// The recipient answers: the issue moves to `answered`, the author is no longer blocked.
	answered, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "fixed in 1a2b3c"})
	if err != nil {
		t.Fatalf("Answer (recipient): %v", err)
	}
	if answered.State != "answered" {
		t.Errorf("state = %q after the recipient answered, want answered", answered.State)
	}

	// The author follows up: the issue goes back to `open`, the recipient owes again.
	reopened, err := st.Answer(ctx, store.Answer{
		Ref:  refFor(web, core, issue.Number),
		Body: "still reproducible at mine",
	})
	if err != nil {
		t.Fatalf("Answer (author): %v", err)
	}
	if reopened.State != "open" {
		t.Errorf("state = %q after the author followed up, want open", reopened.State)
	}

	// The thread holds all three messages, in order.
	found, err := st.IssueByRef(ctx, ref)
	if err != nil {
		t.Fatalf("IssueByRef: %v", err)
	}
	messages, total, err := st.ListMessages(ctx, ref, found.ID, 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("%d messages, want 3", len(messages))
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if messages[0].AuthorKey != "WEB" || messages[1].AuthorKey != "CORE" {
		t.Errorf("thread authors = %s then %s, want WEB then CORE",
			messages[0].AuthorKey, messages[1].AuthorKey)
	}

	// Closing, then refusal of any later answer: without that guard, a late reply would resurrect a
	// finished discussion in the correspondent's inbox.
	closed, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "closing this", Close: true})
	if err != nil {
		t.Fatalf("Answer (closing): %v", err)
	}
	if closed.State != "closed" {
		t.Errorf("state = %q after closing, want closed", closed.State)
	}

	if _, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "one more word"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("answering a closed issue: error = %v, want ErrNotFound", err)
	}
}

// The heart of the product: a third-party project of the SAME team sees nothing of a conversation
// it takes no part in, and cannot write into it.
func TestIssuesAreInvisibleToThirdProjects(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	spy := newProject(t, db, teamID, "SPY")

	// SPY is trusted with CORE: this test proves a conversation's invisibility, not the graph.
	// Without that edge, the read assertions would stay green through a plain lack of write
	// permission, which would mask the property they exist to establish.
	trust(t, db, web, core)
	trust(t, db, spy, core)

	issue := open(t, st, web, core, "private question between WEB and CORE")
	spyRef := refFor(spy, core, issue.Number)

	t.Run("read", func(t *testing.T) {
		if _, err := st.IssueByRef(ctx, spyRef); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY reads the issue: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("listing", func(t *testing.T) {
		issues, err := st.ListIssues(ctx, store.IssueFilter{
			TeamID: teamID, ProjectID: spy.id, Limit: 50,
		})
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 0 {
			t.Errorf("SPY lists %d issues, want 0", len(issues))
		}
	})

	t.Run("message thread", func(t *testing.T) {
		messages, total, err := st.ListMessages(ctx, spyRef, issue.ID, 50)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if total != 0 {
			t.Errorf("SPY reads a total of %d, want 0: the counter must not leak outside the scope", total)
		}
		if len(messages) != 0 {
			t.Errorf("SPY reads %d messages, want 0 (even knowing the identifier)", len(messages))
		}
	})

	t.Run("answer", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{Ref: spyRef, Body: "letting myself in"}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY answers: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("closing", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{Ref: spyRef, Body: "closing it", Close: true}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY closes: error = %v, want ErrNotFound", err)
		}
	})

	// The conversation is untouched for both of its participants.
	for _, caller := range []project{web, core} {
		still, err := st.IssueByRef(ctx, refFor(caller, core, issue.Number))
		if err != nil {
			t.Fatalf("IssueByRef for %s: %v", caller.key, err)
		}
		if still.State != "open" {
			t.Errorf("the issue seen by %s is in %q, want open", caller.key, still.State)
		}
	}
}

// An issue never crosses a team boundary, and an attempt must reveal neither the existence of the
// target project — nor push its counter forward.
func TestIssuesCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := newTeam(t, db)
	teamB := newTeam(t, db)
	web := newProject(t, db, teamA, "WEB")
	sibling := newProject(t, db, teamA, "OPS")
	foreign := newProject(t, db, teamB, "CORE")

	// WEB is trusted with a sibling of ITS OWN team. Without that edge, this test would stay green
	// for the wrong reason: the empty graph would mask the team boundary it exists to prove, and
	// would make it indifferent to a scope regression.
	trust(t, db, web, sibling)

	// The boundary is not merely an absence of results: the cross-team pair is NOT INSERTABLE,
	// whatever team_id is passed. No human can therefore open that channel.
	for _, claimed := range []struct {
		name   string
		teamID uuid.UUID
	}{
		{"sender's team", teamA},
		{"recipient's team", teamB},
	} {
		if _, err := db.Exec(
			`INSERT INTO project_trust (team_id, from_project_id, to_project_id) VALUES ($1, $2, $3)`,
			claimed.teamID, web.id, foreign.id,
		); err == nil {
			t.Fatalf("cross-team edge inserted while claiming the %s, want a foreign key violation", claimed.name)
		}
	}

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamA,
			AuthorProjectID: web.id,
			ToProjectKey:    "CORE", // exists, but in team B
			Title:           "team crossing",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue towards another team: error = %v, want ErrNotFound", err)
	}

	// A key that exists nowhere fails the SAME way: "does not exist" and "outside the team" stay
	// indistinguishable, otherwise one could map out the projects of other teams.
	err = st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamA,
			AuthorProjectID: web.id,
			ToProjectKey:    "NOPE",
			Title:           "non-existent key",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue towards an unknown key: error = %v, want ErrNotFound", err)
	}

	// The foreign project's counter has not moved: it cannot be pushed forward from a distance.
	var next int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", foreign.id).Scan(&next); err != nil {
		t.Fatalf("reading the counter: %v", err)
	}
	if next != 1 {
		t.Errorf("foreign project counter = %d, want 1 (no number consumed)", next)
	}
}

// Tasks and issues share the project counter: a reference always names one single object.
func TestIssuesAndTasksShareTheProjectCounter(t *testing.T) {
	st, db := newStore(t)
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, web, core)

	first := open(t, st, web, core, "first issue at CORE")
	if first.Number != 1 {
		t.Fatalf("number of the first issue = %d, want 1", first.Number)
	}

	// A task created in CORE afterwards takes the next number, not the same one.
	var claimed int64
	if err := db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1",
		core.id,
	).Scan(&claimed); err != nil {
		t.Fatalf("reserving a task number: %v", err)
	}
	if claimed != 2 {
		t.Errorf("task number = %d, want 2 (counter shared with issues)", claimed)
	}

	second := open(t, st, web, core, "second issue at CORE")
	if second.Number != 3 {
		t.Errorf("number of the second issue = %d, want 3", second.Number)
	}

	// WEB's own counter has not moved: every project has its own.
	var webNext int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", web.id).Scan(&webNext); err != nil {
		t.Fatalf("reading WEB's counter: %v", err)
	}
	if webNext != 1 {
		t.Errorf("WEB counter = %d, want 1: opening an issue consumes the recipient's number", webNext)
	}
}

// A question to one's own project makes no sense: it would be incoming and outgoing at once, and
// could never reach `answered` since the transition depends on the sender.
//
// Since the trust graph, the refusal is ErrNotFound rather than ErrConflict: self-addressing gives
// from = to, a shape project_trust_not_self makes NOT INSERTABLE, hence never present in the graph.
// The issues_not_self CHECK is no longer reached — the refusal becomes uniform with that of an
// unknown key, of another team or of an undeclared direction, and that is the point.
func TestSelfIssueIsRejectedByTheDatabase(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")

	// WEB has a declared neighbour: the refusal below therefore cannot come from an empty graph.
	trust(t, db, web, core)

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: web.id,
			ToProjectKey:    "WEB",
			Title:           "talking to myself",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue towards oneself: error = %v, want ErrNotFound (no self-edge is insertable)", err)
	}

	// The self-edge is refused by the database itself: the property above does not rest on a writing
	// convention, but on a constraint.
	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, from_project_id, to_project_id) VALUES ($1, $2, $2)`,
		teamID, web.id,
	); err == nil {
		t.Fatal("a self-edge was inserted, want a project_trust_not_self violation")
	}

	// And WEB's counter has not moved: a refusal consumes no number, including when sender and
	// recipient are the same project.
	var next int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", web.id).Scan(&next); err != nil {
		t.Fatalf("reading WEB's counter: %v", err)
	}
	if next != 1 {
		t.Errorf("WEB counter = %d, want 1 (no number consumed by a refused self-addressing)", next)
	}
}

func TestListIssuesFiltersByRoleAndState(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	// BOTH directions, because this test opens an issue each way. Under the pair of 000007 the
	// single fixture line below covered both; since 000013 it covers one, and the second `open`
	// would fail on a `not found` that has nothing to do with what is under test here.
	trust(t, db, web, core)
	trust(t, db, core, web)

	outgoing := open(t, st, web, core, "WEB asks CORE")
	open(t, st, core, web, "CORE asks WEB")

	base := store.IssueFilter{TeamID: teamID, ProjectID: web.id, Limit: 50}

	t.Run("both directions, no filter", func(t *testing.T) {
		issues, err := st.ListIssues(ctx, base)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 2 {
			t.Fatalf("%d issues, want 2", len(issues))
		}
	})

	t.Run("incoming", func(t *testing.T) {
		filter := base
		filter.Role = "incoming"
		issues, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 || !issues[0].Incoming {
			t.Fatalf("%d incoming issues for WEB, want 1 marked incoming", len(issues))
		}
	})

	t.Run("outgoing", func(t *testing.T) {
		filter := base
		filter.Role = "outgoing"
		issues, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 || issues[0].Incoming {
			t.Fatalf("%d outgoing issues for WEB, want 1 marked outgoing", len(issues))
		}
		if issues[0].ProjectKey != "CORE" {
			t.Errorf("reference key = %s, want CORE (the recipient owns the issue)",
				issues[0].ProjectKey)
		}
	})

	t.Run("closed ones are excluded by default", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{
			Ref:   refFor(web, core, outgoing.Number),
			Body:  "dropping it",
			Close: true,
		}); err != nil {
			t.Fatalf("closing: %v", err)
		}

		issues, err := st.ListIssues(ctx, base)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 {
			t.Errorf("%d active issues, want 1", len(issues))
		}

		filter := base
		filter.IncludeClosed = true
		all, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(all) != 2 {
			t.Errorf("%d issues in total, want 2", len(all))
		}
	})
}

// The role narrows what is already visible; it can never widen it. A third-party project asking
// for "the incoming ones" must not thereby see other people's.
func TestRoleFilterNeverWidensVisibility(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	spy := newProject(t, db, teamID, "SPY")

	// Same reason as in the previous test: SPY is trusted with CORE, so what it does not see, it
	// does not see because it takes no part — not because it lacks the right to write.
	trust(t, db, web, core)
	trust(t, db, spy, core)

	open(t, st, web, core, "private conversation")

	for _, role := range []string{"", "incoming", "outgoing"} {
		issues, err := st.ListIssues(ctx, store.IssueFilter{
			TeamID: teamID, ProjectID: spy.id, Role: role, IncludeClosed: true, Limit: 50,
		})
		if err != nil {
			t.Fatalf("ListIssues(role=%q): %v", role, err)
		}
		if len(issues) != 0 {
			t.Errorf("SPY sees %d issues with role=%q, want 0", len(issues), role)
		}
	}
}

// Nesting transactions must fail loudly: a second transaction would wait, on another connection,
// for the lock the first one holds.
func TestNestedTransactionIsRefused(t *testing.T) {
	st, _ := newStore(t)

	err := st.WithTx(context.Background(), func(tx store.Store) error {
		return tx.WithTx(context.Background(), func(store.Store) error { return nil })
	})
	if err == nil {
		t.Fatal("a nested transaction was accepted")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("error = %v, want an explicit refusal of nesting", err)
	}
}

// Mirror of TestCancelledRequestCreatesNothing on the issue side, on the costliest path: a
// duplicated issue pollutes ANOTHER repo's inbox. When the client gives up, the context is
// cancelled and the transaction with it — no issue, and above all no number consumed at the
// recipient's, whose counter does not belong to the sender.
func TestCancelledRequestOpensNothing(t *testing.T) {
	st, db := newStore(t)
	teamID := newTeam(t, db)
	frnt := newProject(t, db, teamID, "FRNT")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, frnt, core)

	ctx, cancel := context.WithCancel(context.Background())
	err := st.WithTx(ctx, func(tx store.Store) error {
		created, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    core.key,
			Title:           "question whose answer will be lost",
		})
		if err != nil {
			return err
		}

		// The client gives up once the recipient's number is reserved.
		cancel()

		return tx.AddFirstMessage(ctx, created.ID, frnt.id, "body of the question")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM issues WHERE team_id = $1", teamID).Scan(&count); err != nil {
		t.Fatalf("counting the issues: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d issue(s) created by a cancelled request, want 0", count)
	}

	// The agent's replay takes number 1: the recipient's counter has not moved.
	replayed := open(t, st, frnt, core, "question whose answer will be lost")
	if replayed.Number != 1 {
		t.Errorf("number = %d after cancellation, want 1 (recipient counter untouched)", replayed.Number)
	}
}

// M5 — the refusal lives in the QUERY, not in a service `if`.
//
// This test short-circuits the service entirely: it builds a store.NewIssue by hand and calls the
// store directly, exactly as a faulty caller from some future module would. It must return
// ErrNotFound all the same. Were the predicate ever to migrate into an upstream `if`, this test
// would be the only one in the repo to go red.
//
// It also proves the edge's DIRECTION, which is the whole of card 11: one row authorises ONE way,
// and the opposite way is refused exactly like an undeclared pair. Under the symmetric table of
// 000007 the last subtest below read "want success (the edge is symmetric)"; it is now the
// headline guarantee, inverted.
func TestTrustPredicateLivesInTheQueryNotInAService(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	core := newProject(t, db, teamID, "CORE")
	ops := newProject(t, db, teamID, "OPS")

	// FRNT → CORE, and that arrow alone. OPS exists, is in the same team, and has no edge at all.
	trust(t, db, frnt, core)

	createDirectly := func(from, to project) error {
		return st.WithTx(ctx, func(tx store.Store) error {
			_, err := tx.CreateIssue(ctx, store.NewIssue{
				TeamID:          from.teamID,
				AuthorProjectID: from.id,
				ToProjectKey:    to.key,
				Title:           "direct store call, service short-circuited",
			})
			return err
		})
	}

	t.Run("undeclared pair", func(t *testing.T) {
		if err := createDirectly(frnt, ops); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT → OPS: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("undeclared pair, reverse direction", func(t *testing.T) {
		if err := createDirectly(ops, frnt); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("OPS → FRNT: error = %v, want ErrNotFound", err)
		}
	})

	// THE TWO SUBTESTS CARD 11 EXISTS FOR. The arrow was laid down FRNT → CORE: that direction goes
	// through, and the opposite one does not — although both projects exist, sit in the same team,
	// and are joined by a row of the graph.
	t.Run("declared direction", func(t *testing.T) {
		if err := createDirectly(frnt, core); err != nil {
			t.Errorf("FRNT → CORE: %v, want success", err)
		}
	})

	t.Run("the reverse of a declared direction is refused", func(t *testing.T) {
		if err := createDirectly(core, frnt); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CORE → FRNT: error = %v, want ErrNotFound — the arrow FRNT → CORE authorises "+
				"FRNT to ask, and nothing else", err)
		}
	})

	// The refusal is indistinguishable from an unknown key: same error, on the same path.
	t.Run("unknown key", func(t *testing.T) {
		err := st.WithTx(ctx, func(tx store.Store) error {
			_, err := tx.CreateIssue(ctx, store.NewIssue{
				TeamID:          teamID,
				AuthorProjectID: frnt.id,
				ToProjectKey:    "NOPE",
				Title:           "key that exists nowhere",
			})
			return err
		})
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT → NOPE: error = %v, want ErrNotFound, identical to an undeclared pair", err)
		}
	})
}

// M2 — a trust refusal leaves NO trace at all.
//
// This is the test the "move the EXISTS onto the INSERT ... SELECT" mutation turns red. Under that
// mutation the UPDATE still matches: the recipient's counter moves forward, and the project row
// stays locked for the whole of the refused sender's transaction — an unauthorised sender would
// gain a write channel at its victim's AND a denial of service on a legitimate third party.
//
// The four counters are read before and after, and compared as-is. No latency assertion: what the
// mutation opens is measured deterministically.
func TestRefusedIssueLeavesNoTrace(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	ops := newProject(t, db, teamID, "OPS")

	type snapshot struct {
		nextNumber                      int64
		issues, messages, events, edges int64
	}
	take := func() snapshot {
		t.Helper()
		var s snapshot
		if err := db.QueryRow(
			`SELECT (SELECT next_number FROM projects WHERE id = $1),
			        (SELECT count(*) FROM issues         WHERE team_id = $2),
			        (SELECT count(*) FROM issue_messages m JOIN issues i ON i.id = m.issue_id WHERE i.team_id = $2),
			        (SELECT count(*) FROM events         WHERE team_id = $2),
			        (SELECT count(*) FROM project_trust  WHERE team_id = $2)`,
			ops.id, teamID,
		).Scan(&s.nextNumber, &s.issues, &s.messages, &s.events, &s.edges); err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		return s
	}

	before := take()

	// THE TRANSACTION IS COMMITTED, and that is the whole point of the test.
	//
	// Returning the error would ROLL BACK, and the rollback would mask the mutation: under "EXISTS
	// moved onto the INSERT", the UPDATE matches, next_number moves forward, then the transaction
	// undoes everything — and the test would stay green while observing a property the predicate
	// does not provide. Channel 3's guarantee is "closed BY THE PREDICATE, safe even if the
	// transaction is committed": so we check it by committing.
	var refusal error
	if err := st.WithTx(ctx, func(tx store.Store) error {
		_, refusal = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    ops.key,
			Title:           "attempt towards an undeclared pair",
		})
		return nil // deliberate commit
	}); err != nil {
		t.Fatalf("the refusal transaction did not commit: %v", err)
	}
	if !errors.Is(refusal, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", refusal)
	}

	after := take()

	if after.nextNumber != before.nextNumber {
		t.Errorf("OPS next_number = %d, want %d: a refusal reserves no number at the victim's",
			after.nextNumber, before.nextNumber)
	}
	if after.issues != before.issues {
		t.Errorf("%d issues, want %d", after.issues, before.issues)
	}
	if after.messages != before.messages {
		t.Errorf("%d messages, want %d", after.messages, before.messages)
	}
	if after.events != before.events {
		t.Errorf("%d events, want %d", after.events, before.events)
	}
	// A refusal NEVER writes into the graph: neither to remember, nor to "learn" the pair. The graph
	// has a single author, the human under an admin token.
	if after.edges != before.edges {
		t.Errorf("%d edges, want %d: a refusal never writes into project_trust", after.edges, before.edges)
	}

	// And the channel stays openable as soon as the human declares the pair: the refusal broke nothing.
	trust(t, db, frnt, ops)
	opened := open(t, st, frnt, ops, "the same question, once the pair is declared")
	if opened.Number != 1 {
		t.Errorf("number = %d after declaration, want 1: the refusal had consumed no number", opened.Number)
	}
}

// M2, second channel — a trust refusal TAKES NO LOCK on the recipient's row.
//
// This is the property that stops an unauthorised repo from running a targeted denial of service on
// a legitimate third party: without it, all it takes is opening a transaction, attempting an issue
// towards its victim and dragging its feet, to block every legitimate creator for that whole time.
// Measured at design time: 1933 ms against 73 ms.
//
// The probe is FOR NO KEY UPDATE ... NOWAIT from a SECOND connection, while the refusal's
// transaction is still open. It is deterministic: either the lock is held and Postgres raises 55P03
// immediately, or it is not and the probe goes through. No latency assertion — a latency test in CI
// is red one day in three, hence a test people switch off.
func TestRefusedIssueLocksNothing(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	ops := newProject(t, db, teamID, "OPS")

	var refusal, probe error
	if err := st.WithTx(ctx, func(tx store.Store) error {
		_, refusal = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    ops.key,
			Title:           "attempt towards an undeclared pair",
		})

		// Still INSIDE the refusal's transaction: that is the only moment the lock would exist. db
		// is a pool, so this query leaves on a different connection from the transaction's.
		var id uuid.UUID
		probe = db.QueryRow(
			"SELECT id FROM projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT", ops.id,
		).Scan(&id)

		return nil
	}); err != nil {
		t.Fatalf("the refusal transaction did not commit: %v", err)
	}

	if !errors.Is(refusal, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", refusal)
	}
	if probe != nil {
		t.Errorf("the OPS row was locked during the refusal (%v): an unauthorised sender can block a "+
			"legitimate creator by dragging its feet inside its transaction", probe)
	}
}

// THE THREAD BOUND IS IN THE QUERY, NOT IN MEMORY.
//
// The service used to slice the result afterwards: the database serialised the WHOLE thread, the
// network carried it, and Go threw away everything but the last messages. The agent's context was
// protected; neither the database, nor the network, nor the process heap were. On an issue the
// bodies are COMPLETE — it is the heaviest thing the product hands back.
//
// This test calls the STORE directly, so it cannot be satisfied by slicing downstream: if the query
// returns everything, it goes red.
//
// MUTATION: removing `LIMIT @lim` from ListIssueMessages makes the "the query bounds" subtest fail.
func TestIssueThreadIsBoundedByTheQuery(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, frnt, core)

	issue := open(t, st, frnt, core, "long thread")
	ref := refFor(core, core, issue.Number)

	// The first message comes from open(); we add 24 more, 25 in total.
	const written = 25
	for i := 1; i < written; i++ {
		caller, target := core, core
		if i%2 == 0 {
			caller = frnt
		}
		if _, err := st.Answer(ctx, store.Answer{
			Ref:  refFor(caller, target, issue.Number),
			Body: fmt.Sprintf("message %d", i),
		}); err != nil {
			t.Fatalf("Answer %d: %v", i, err)
		}
	}

	t.Run("the query bounds", func(t *testing.T) {
		messages, total, err := st.ListMessages(ctx, ref, issue.ID, 10)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if len(messages) != 10 {
			t.Errorf("%d messages returned for a limit of 10: the bound is not in the query",
				len(messages))
		}
		// The total is that of the WHOLE thread, not of the window: without it, an agent would
		// believe it had read the entire conversation.
		if total != written {
			t.Errorf("total = %d, want %d: the counter must cover the whole thread", total, written)
		}
	})

	t.Run("the window is the TAIL of the thread, in write order", func(t *testing.T) {
		messages, _, err := st.ListMessages(ctx, ref, issue.ID, 10)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if len(messages) != 10 {
			t.Fatalf("%d messages, want 10", len(messages))
		}
		// The last ones written are messages 15 to 24.
		if messages[0].Body != "message 15" {
			t.Errorf("first message of the window = %q, want \"message 15\": it is the LAST "+
				"messages that carry the state", messages[0].Body)
		}
		if messages[9].Body != "message 24" {
			t.Errorf("last message = %q, want \"message 24\": the thread must come back out in "+
				"write order", messages[9].Body)
		}
		for i := 1; i < len(messages); i++ {
			if messages[i].CreatedAt.Before(messages[i-1].CreatedAt) {
				t.Fatalf("the thread is not in chronological order at index %d", i)
			}
		}
	})

	t.Run("a limit wider than the thread returns everything", func(t *testing.T) {
		messages, total, err := st.ListMessages(ctx, ref, issue.ID, 1000)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if len(messages) != written || total != written {
			t.Errorf("%d messages / total %d, want %d: a wide bound must remove nothing",
				len(messages), total, written)
		}
	})
}
