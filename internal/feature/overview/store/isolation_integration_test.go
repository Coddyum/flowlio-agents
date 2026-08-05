package store_test

// The ISOLATION guarantees, one per test, each with the mutation that must make it fall over.
// Fixture, helpers and set assertions: store_integration_test.go.
//
// A GUARANTEE WITH NO MUTATION THAT KILLS IT IS AN INTENTION, NOT A GUARANTEE. Every mutation
// written as a comment here was PLAYED, and the matching test went red. The ones that are not
// killable are said to be so, with their reason — that is the lesson FLWL-21 cost.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	overviewstore "github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// longAgo is a fixed, old time base: a queue is read by age, so the test's dates must depend
// neither on the machine's clock nor on the order of execution.
var longAgo = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// GUARANTEE 5 — no issue of another team in the queue.
//
// The assertion bears on the EXACT SET. "contains nothing of B" would also pass on an empty
// result, that is to say on a query that no longer yields anything at all.
//
// MUTATION PLAYED: remove `i.team_id = @team_id` from OverviewIssueDebts → A's queue carries B's
// issues, this test goes red.
func TestOverviewNeverCrossesTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "A asks CORE", "open", longAgo)
	openIssue(t, db, f.a, "WEB", "CORE", 2, "A asks WEB", "answered", longAgo)
	openIssue(t, db, f.b, "CORE", "WEB", 3, "B asks CORE", "open", longAgo)
	openIssue(t, db, f.b, "WEB", "CORE", 4, "B asks WEB", "open", longAgo)

	debts, total, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1", "WEB-2")
	if total != 2 {
		t.Errorf("total = %d, expected 2 — the announced count overflows the team", total)
	}
}

// GUARANTEE 6 — positive control of 5: the queue sees ALL the debt of its team.
//
// Without it, a query that would never yield anything would pass the isolation test with
// honours. Both in-flight states are represented, `closed` is not.
//
// MUTATION PLAYED: replace the team parameter with uuid.Nil → zero rows, this test goes red.
func TestOverviewSeesEveryDebtOfItsTeam(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "waiting for an answer", "open", longAgo)
	openIssue(t, db, f.a, "WEB", "CORE", 2, "answered, not consumed", "answered", longAgo)
	if _, err := db.Exec(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, closed_at)
		 VALUES ($1, $2, $3, 3, 'closed one', 'closed', now())`,
		f.a.id, f.a.projects["CORE"], f.a.projects["DOCS"],
	); err != nil {
		t.Fatalf("creating the closed issue: %v", err)
	}

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1", "WEB-2")
}

// GUARANTEE 7 — the join towards projects does not cross the team.
//
// Both teams have a `CORE`: a scope bearing on the KEY alone would pass every other test and
// would fail here. That is the sole reason for the homonymous fixture.
//
// MUTATION DECLARED NOT KILLABLE, AND SAYING SO IS BETTER THAN WRITING A TEST THAT LIES: removing
// `AND p.team_id = i.team_id` from the join changes nothing observable, because the composite FK
// `issues_project_fk (project_id, team_id) → projects(id, team_id)` makes the clause
// mathematically redundant — no insertable dataset puts it at fault. The clause stays written
// (defence in depth if the project resolution ever changes) but the real control is the FK, and
// that is what is tested, in guarantee 8.
//
// This test keeps what IS observable: A's queue carries nothing but A's rows, even though the
// project keys are indistinguishable between the two teams.
func TestOverviewJoinIsTeamScoped(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "at A", "open", longAgo)
	openIssue(t, db, f.b, "CORE", "WEB", 2, "at B, same project key", "open", longAgo)

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1")
}

// GUARANTEE 8 — the composite FK is the REAL control on issues.
//
// It is what makes the mutation of guarantee 7 unobservable, so it is what must be tested: an
// issue of team A cannot designate a project of team B, the database rejects it.
//
// MUTATION: drop `issues_project_fk` from the migration → the insert goes through, this test goes
// red.
func TestIssueCannotReferenceForeignProject(t *testing.T) {
	_, db := newStore(t)
	f := newFixture(t, db)

	_, err := db.Exec(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title)
		 VALUES ($1, $2, $3, 1, 'issue of A pointing at a project of B')`,
		f.a.id, f.b.projects["CORE"], f.a.projects["WEB"],
	)
	if err == nil {
		t.Fatal("insert accepted: an issue can designate another team's project — the composite " +
			"FK no longer holds, and the team clause of the joins becomes load-bearing")
	}
}

// GUARANTEE 9 — the counters ignore the other teams.
//
// Ten more issues at the neighbour's, and not one counter of A moves. The scalar subqueries are
// correlated on `p.team_id`, the row already matched, and not on the parameter.
//
// THE MUTATION ANNOUNCED BY THE NOTE DOES NOT KILL THIS TEST, and it is the note that is wrong.
// It said: "remove `i.team_id = p.team_id` from a scalar subquery". Played, it leaves the suite
// GREEN — the subquery is correlated on `p.id`, a UUID unique across the whole installation, and
// the composite FK forbids an issue of B from carrying the project_id of a project of A. The team
// predicate is therefore mathematically redundant there, exactly as in the joins of guarantee 7.
//
// THE MUTATION THAT REALLY KILLS IT, PLAYED: remove `p.team_id = @team_id` from the WHERE of
// OverviewProjects → A's screen carries B's repos, this test goes red (and guarantee 11's with
// it). That predicate, and it alone, is what scopes the counters.
func TestOverviewCountersAreTeamScoped(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "A's only debt", "open", longAgo)
	for n := int64(10); n < 20; n++ {
		openIssue(t, db, f.b, "CORE", "WEB", n, "the neighbour's debt", "open", longAgo)
	}

	counters, err := st.Projects(ctx, f.a.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	for _, c := range counters {
		want := int64(0)
		if c.Key == "CORE" {
			want = 1
		}
		if c.OwesAnswer != want {
			t.Errorf("%s.owes_answer = %d, expected %d — the counters overflow the team",
				c.Key, c.OwesAnswer, want)
		}
	}
}

// GUARANTEE 10 — a repo with nothing in flight stays on screen.
//
// It is the worst possible failure on this screen, because it is silent: a supervisor cannot look
// for what they cannot see. The property is structural — scalar subqueries and not a join — and
// this test guards it.
//
// MUTATION PLAYED: replace a scalar subquery with `JOIN issues i ON i.project_id = p.id` → DOCS
// disappears, this test goes red.
func TestOverviewKeepsIdleProjects(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "the team's only activity", "open", longAgo)

	counters, err := st.Projects(ctx, f.a.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	seen := map[string]overviewstore.ProjectCounters{}
	for _, c := range counters {
		seen[c.Key] = c
	}

	docs, ok := seen["DOCS"]
	if !ok {
		t.Fatal("DOCS missing from the screen — a repo with nothing in flight disappeared, and " +
			"the supervisor cannot look for what they cannot see")
	}
	if docs.OwesAnswer+docs.AwaitingAnswer+docs.AnsweredUnread+docs.TasksRunning+docs.TasksBlocked != 0 {
		t.Errorf("DOCS is not at zero: %+v", docs)
	}
}

// GUARANTEE 11 — the list of projects is NEVER truncated.
//
// Forty repos and a hundred debts: the debts get bounded, the projects do not.
//
// MUTATION: apply the bound to OverviewProjects → fewer than 40 rows, this test goes red.
func TestOverviewNeverTruncatesProjects(t *testing.T) {
	st, db := newStore(t)

	keys := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		keys = append(keys, fmt.Sprintf("P%02d", i))
	}
	tm := newTeam(t, db, keys...)

	for n := int64(1); n <= 100; n++ {
		dest := keys[n%40]
		author := keys[(n+1)%40]
		openIssue(t, db, tm, dest, author, n, "debt", "open", longAgo)
	}

	counters, err := st.Projects(ctx, tm.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	if len(counters) != 40 {
		t.Errorf("projects[] = %d rows, expected 40 — the list of repos is truncated", len(counters))
	}
}

// GUARANTEE 12 — another team's thread cannot be found.
//
// The refusal is an ErrNotFound, indistinguishable from a reference that does not exist.
//
// MUTATION PLAYED: remove `i.team_id = @team_id` from OverviewIssueByRef → B's thread opens from
// A, this test goes red.
func TestOverviewThreadCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.b, "CORE", "WEB", 7, "the neighbour's thread", "open", longAgo)

	_, err := st.IssueByRef(ctx, f.a.id, "CORE", 7)
	if !errors.Is(err, overviewstore.ErrNotFound) {
		t.Fatalf("err = %v, expected ErrNotFound — another team's thread is readable", err)
	}
}

// GUARANTEE 13 — positive control of 12: the overview reads a thread it is NEITHER the author NOR
// the recipient of.
//
// It is the new capability of this surface, and without this test guarantee 12 would pass on a
// query that never yields anything. It is also exactly what makes these routes forbidden to a
// project token.
//
// MUTATION: add `AND (i.project_id = @caller OR i.author_project_id = @caller)` to
// OverviewIssueByRef, as in GetIssueByRef → this test goes red.
func TestOverviewThreadIsVisibleToNeitherParticipant(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 41, "Has the /v1/sessions contract changed?", "open", longAgo)

	issue, err := st.IssueByRef(ctx, f.a.id, "CORE", 41)
	if err != nil {
		t.Fatalf("IssueByRef: %v", err)
	}
	if issue.ProjectKey != "CORE" || issue.AuthorProjectKey != "WEB" {
		t.Errorf("thread read = %s←%s, expected CORE←WEB", issue.ProjectKey, issue.AuthorProjectKey)
	}
}

// GUARANTEE 14 — THE LOAD-BEARING CLAUSE. Messages do not leak through their author.
//
// issue_messages has no team_id and its FK towards projects is SIMPLE: nothing at the schema
// level stops `author_project_id` from pointing at a project of another team. It is the ONLY
// occurrence in the repo where removing a join clause is observable, hence the only one that can
// really be tested.
//
// MUTATION PLAYED: remove `AND ap.team_id = i.team_id` from OverviewIssueMessages → the foreign
// message comes up, this test goes red.
func TestOverviewMessagesRejectForeignAuthor(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	issueID := openIssue(t, db, f.a, "CORE", "WEB", 1, "A's thread", "open", longAgo)
	addMessage(t, db, issueID, f.a.projects["WEB"], "legitimate message", longAgo)
	addMessage(t, db, issueID, f.b.projects["WEB"], "message from a project of another team", longAgo.Add(time.Minute))

	messages, total, err := st.IssueMessages(ctx, f.a.id, issueID, 200)
	if err != nil {
		t.Fatalf("IssueMessages: %v", err)
	}

	if len(messages) != 1 || total != 1 {
		t.Fatalf("%d message(s) rendered, total %d, expected 1 and 1 — a message whose author "+
			"belongs to another team leaked", len(messages), total)
	}
	if messages[0].BodyMd != "legitimate message" {
		t.Errorf("message rendered = %q, expected the legitimate one", messages[0].BodyMd)
	}
}

// GUARANTEE 15 — another team's backlog cannot be found.
//
// MUTATION PLAYED: remove `t.team_id = @team_id` from OverviewTaskDebts → B's tasks come up, this
// test goes red.
func TestOverviewTaskDebtsCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	addTask(t, db, f.a, "CORE", 1, "blocked at A", "blocked", longAgo)
	addTask(t, db, f.b, "CORE", 2, "blocked at B", "blocked", longAgo)
	addTask(t, db, f.b, "WEB", 3, "blocked at B too", "blocked", longAgo)

	debts, total, err := st.TaskDebts(ctx, f.a.id, longAgo.Add(time.Hour), 50)
	if err != nil {
		t.Fatalf("TaskDebts: %v", err)
	}

	assertSet(t, taskRefs(debts), "CORE-1")
	if total != 1 {
		t.Errorf("total = %d, expected 1", total)
	}
}

// GUARANTEE 16 — the announced total counts what the bound hides.
//
// It is the most likely flaw of the lot: without it a truncated list lies, and the screen is
// wrong in a silent, credible way. The `count(*) OVER ()` counts BEFORE the LIMIT, in the same
// query, hence over the same snapshot.
//
// MUTATION PLAYED: drop `count(*) OVER ()` and yield 0 → total = 0, this test goes red.
func TestOverviewTruncatedCountsWhatIsHidden(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	for n := int64(1); n <= 60; n++ {
		openIssue(t, db, f.a, "CORE", "WEB", n, "debt", "open", longAgo.Add(time.Duration(n)*time.Second))
	}

	debts, total, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	if len(debts) != 50 {
		t.Errorf("%d rows rendered, expected 50 — the bound no longer applies", len(debts))
	}
	if total != 60 {
		t.Errorf("total = %d, expected 60 — the number of hidden debts is wrong, and the screen "+
			"lies without saying so", total)
	}
}

// The queue yields the OLDEST debt first — the reverse of ListIssues, and that is deliberate: an
// agent wants what is fresh, a supervisor wants what is rotting.
//
// MUTATION: switch the ORDER BY of OverviewIssueDebts to DESC → this test goes red.
func TestOverviewOldestDebtComesFirst(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "recent", "open", longAgo.Add(72*time.Hour))
	openIssue(t, db, f.a, "WEB", "CORE", 2, "the oldest", "open", longAgo)

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	if len(debts) != 2 || debts[0].Number != 2 {
		t.Fatalf("first row = %+v, expected WEB-2 — the queue no longer brings up what is rotting", debts)
	}
}

// A repo's pulse is the last call of one of ITS tokens. A repo no token of which has served has
// no row at all — and not a zero date, which would read like a date.
//
// The admin's traffic does not count: an admin token carries neither team nor project since
// migration 000006, and the JOIN on project_id excludes it anyway. The human is not an agent, and
// their own traffic must not make a repo look alive.
func TestOverviewPulseIgnoresAdminAndSilentProjects(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	seenAt := longAgo.Add(3 * time.Hour)
	addToken(t, db, f.a, "CORE", &seenAt)
	addToken(t, db, f.a, "WEB", nil)
	addToken(t, db, f.a, "", &seenAt)

	pulses, err := st.LastSeen(ctx, f.a.id)
	if err != nil {
		t.Fatalf("LastSeen: %v", err)
	}

	if len(pulses) != 1 {
		t.Fatalf("%d pulses rendered, expected 1 (CORE alone): %+v", len(pulses), pulses)
	}
	if pulses[0].Key != "CORE" || !pulses[0].LastSeen.Equal(seenAt) {
		t.Errorf("pulse = %+v, expected CORE at %s", pulses[0], seenAt)
	}
}

// `last_move` is NOT `updated_at`: it is the most recent of the two between the task and its last
// note.
//
// CreateTaskNote does not write `tasks.updated_at` (sql/queries/tasks.sql). Without the `max()`
// over the notes, an agent actively documenting its progress would be reported as a "dead
// session" — the costliest false positive of this screen, because it pushes a human to interrupt
// the one agent that is working correctly.
//
// MUTATION: replace `greatest(t.updated_at, coalesce(max(note), t.updated_at))` with
// `t.updated_at` → the documented task reappears in the debts, this test goes red.
func TestOverviewLastMoveCountsNotes(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	addTask(t, db, f.a, "CORE", 1, "abandoned for good", "in_progress", longAgo)
	documented := addTask(t, db, f.a, "CORE", 2, "moving on, and saying so", "in_progress", longAgo)
	addNote(t, db, documented, "I isolated the cause, I am fixing it", longAgo.Add(30*time.Hour))

	debts, _, err := st.TaskDebts(ctx, f.a.id, longAgo.Add(24*time.Hour), 50)
	if err != nil {
		t.Fatalf("TaskDebts: %v", err)
	}

	assertSet(t, taskRefs(debts), "CORE-1")

	notes, total, err := st.TaskNotes(ctx, f.a.id, documented, 50)
	if err != nil {
		t.Fatalf("TaskNotes: %v", err)
	}
	if len(notes) != 1 || total != 1 {
		t.Fatalf("%d note(s), total %d, expected 1 and 1", len(notes), total)
	}
}

// Reading an empty scope yields nothing. It is the counter-proof of the service's defence in
// depth: even if a uuid.Nil reached the store, it would match no row.
func TestOverviewNilTeamReadsNothing(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "A's debt", "open", longAgo)

	debts, total, err := st.IssueDebts(ctx, uuid.Nil, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}
	if len(debts) != 0 || total != 0 {
		t.Errorf("%d row(s) for a nil scope, expected 0", len(debts))
	}
}
