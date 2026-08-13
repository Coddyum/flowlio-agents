package feature_test

// THE COST MODEL OF THE WAKE PROBE, PROVED AND NOT ASSERTED (D55, docs/DESIGN-WAKE.md §3, §11.1).
//
// The whole point of the probe is that asking "is there anything for me?" costs nothing in steady
// state: an integer-vs-integer compare held in memory, never a query. This file counts the queries
// the probe issues and holds it to zero across a hundred empty probes — then lets a sibling write a
// real event and shows the same probe flip to "yes" WITHOUT a query, because the event's writer
// bumped the in-memory head as it wrote.
//
// WHY IT COUNTS AND DOES NOT MOCK. The count is wired at the DBTX seam: every query the wake store
// would send to Postgres passes through countingDBTX first. Removing the cache — making the probe
// read the cursor from the database every time — takes the hundred-probe count from 0 to 100, and
// this test goes red. That is the mutation the design names.
//
// WHY THE EVENT GOES THROUGH THE ISSUE STORE AND NOT A RAW INSERT. The head bump lives in
// AppendEvent, on the write path a real answer takes. Driving that path is what proves the chain
// end to end: a sibling's answer → the durable event → the in-memory head → the probe the waker
// reads. A raw INSERT would write the row and prove none of the wiring this card exists for.

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/Coddyum/flowlio-agents/internal/database"
	issuestore "github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	wakeservice "github.com/Coddyum/flowlio-agents/internal/feature/wake/service"
	wakestore "github.com/Coddyum/flowlio-agents/internal/feature/wake/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// countingDBTX counts every query sent through it before handing it to the real database. It is the
// instrument the zero-SQL property is measured with.
type countingDBTX struct {
	inner database.DBTX
	n     *int64
}

func (c countingDBTX) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	atomic.AddInt64(c.n, 1)
	return c.inner.ExecContext(ctx, q, args...)
}

func (c countingDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	atomic.AddInt64(c.n, 1)
	return c.inner.PrepareContext(ctx, q)
}

func (c countingDBTX) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	atomic.AddInt64(c.n, 1)
	return c.inner.QueryContext(ctx, q, args...)
}

func (c countingDBTX) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	atomic.AddInt64(c.n, 1)
	return c.inner.QueryRowContext(ctx, q, args...)
}

// TestWakeProbeIsFreeInSteadyStateThenSeesARealEvent is the acceptance of phase 1.
func TestWakeProbeIsFreeInSteadyStateThenSeesARealEvent(t *testing.T) {
	db, f := newFixture(t)
	ctx := context.Background()

	// A project token for CORE: the probe's scope, cursor included, is entirely this token's.
	var tokenID uuid.UUID
	prefix := strings.ToLower(uuid.NewString()[:8]) + "wxyz"
	if err := db.QueryRow(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope)
		 VALUES ($1, $2, 'agent', $3, 'test-hash', 'project') RETURNING id`,
		f.teamID, f.projectID, prefix,
	).Scan(&tokenID); err != nil {
		t.Fatalf("creating the token: %v", err)
	}

	// One cache shared by both sides, exactly as ModuleConfig shares it: the issue store bumps the
	// head into it, the wake service reads the head out of it.
	c := cache.NewMemory(time.Hour, time.Hour)

	var queries int64
	counted := database.New(countingDBTX{inner: db, n: &queries})
	wsvc := wakeservice.New(wakestore.New(counted), c)
	issues := issuestore.New(database.New(db), db, c)

	in := wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: f.projectID, TokenID: tokenID}

	// The first probe finds a cold cache and seeds it from one read.
	r, err := wsvc.Probe(ctx, in)
	if err != nil {
		t.Fatalf("cold probe: %v", err)
	}
	if r.HasWork {
		t.Fatal("an empty project reported work")
	}
	if atomic.LoadInt64(&queries) == 0 {
		t.Fatal("the cold probe issued no query — it did not seed itself from the database")
	}

	// A hundred more probes must not add a single query. Whether the cache answers the compare from
	// memory or the escalation ladder throttles a too-eager caller, NEITHER path touches Postgres —
	// which is the whole cost property. Remove the cache and every one of these cold-reads instead.
	atomic.StoreInt64(&queries, 0)
	for i := 0; i < 100; i++ {
		r, err := wsvc.Probe(ctx, in)
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		if r.HasWork {
			t.Fatalf("probe %d reported work on an untouched project", i)
		}
	}
	if got := atomic.LoadInt64(&queries); got != 0 {
		t.Fatalf("100 empty probes issued %d queries, want 0 — the steady-state probe is hitting Postgres", got)
	}

	// A sibling answers a REAL question this project asked: the issue exists, is now `answered`, and
	// the event names it. The event bumps this project's head; the issue is what makes the movement
	// actionable — a probe launches for a real answer, not for a bare event (FLWL-85).
	var siblingID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'SIBL', 'Project SIBL') RETURNING id",
		f.teamID,
	).Scan(&siblingID); err != nil {
		t.Fatalf("creating the sibling project: %v", err)
	}
	var answeredIssue uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state)
		 VALUES ($1, $2, $3, 1, 'a question I asked', 'answered') RETURNING id`,
		f.teamID, siblingID, f.projectID,
	).Scan(&answeredIssue); err != nil {
		t.Fatalf("inserting the answered issue: %v", err)
	}
	if err := issues.WithTx(ctx, func(tx issuestore.Store) error {
		return tx.AppendEvent(ctx, issuestore.Event{
			TeamID:          f.teamID,
			ProjectID:       siblingID,
			ActorProjectID:  siblingID,
			NotifyProjectID: f.projectID,
			Kind:            issuestore.KindIssueAnswered,
			SubjectID:       answeredIssue,
		})
	}); err != nil {
		t.Fatalf("appending the event: %v", err)
	}

	// The head bump reached the SHARED cache: this project's head is now strictly above this token's
	// cursor, entirely in memory, no query. This is the wiring the wake path rests on —
	// AppendEvent → probe head → the compare a probe makes.
	head, headWarm := probe.Head(c, f.teamID, f.projectID)
	cursor, cursorWarm := probe.Cursor(c, tokenID)
	if !headWarm || !cursorWarm {
		t.Fatal("the shared probe signal went cold — the head bump did not reach the cache")
	}
	if head <= cursor {
		t.Fatalf("cached head %d is not above cursor %d — the sibling's event is invisible to the probe", head, cursor)
	}

	// End to end through a FRESH token, which the escalation ladder has never throttled: its probe
	// evaluates the compare and reports the work the sibling wrote.
	var fresh uuid.UUID
	freshPrefix := strings.ToLower(uuid.NewString()[:8]) + "frsh"
	if err := db.QueryRow(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope)
		 VALUES ($1, $2, 'agent2', $3, 'test-hash', 'project') RETURNING id`,
		f.teamID, f.projectID, freshPrefix,
	).Scan(&fresh); err != nil {
		t.Fatalf("creating the fresh token: %v", err)
	}
	r, err = wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: f.projectID, TokenID: fresh})
	if err != nil {
		t.Fatalf("fresh-token probe: %v", err)
	}
	if !r.HasWork {
		t.Fatal("a fresh token's probe did not see the event a sibling wrote")
	}
}

// TestAnsweringAnIssueDoesNotWakeTheAnswerer is the regression for the empty second wake (FLWL-82).
//
// The waker log that opened the card: a repo answered an issue, then woke a SECOND time for nothing.
// The cause was a team-wide probe head — a repo's own answer bumped it above the repo's own cursor,
// so the next probe reported work that was the repo's own write. The head is now per-project, keyed
// by the event's notify target, so an answer addressed to the OTHER party never lifts the answerer's
// own head.
//
// It reads the head through wakestore.Position — the cold read the probe seeds from — because that
// is where the per-project SQL lives, and because it is free of the escalation ladder that a
// repeated same-token Probe would throttle. Position reports work when Head is strictly above Cursor.
//
// The scenario is the exact one from the log, driven through the real AppendEvent path:
//   - AGNT opens a question to CORE  → event addressed to CORE (CORE has work, AGNT does not)
//   - CORE reads its inbox           → its cursor catches its head
//   - CORE answers                   → event addressed to AGNT (CORE's own write)
//   - CORE's head                    → UNMOVED (the bug lifted it here → empty second wake)
//   - AGNT's head                    → carries the answer
func TestAnsweringAnIssueDoesNotWakeTheAnswerer(t *testing.T) {
	db, f := newFixture(t)
	ctx := context.Background()

	// A second project in the same team: AGNT, the author. The fixture already made CORE (f.projectID),
	// the recipient that will answer.
	var agntID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'AGNT', 'Project AGNT') RETURNING id",
		f.teamID,
	).Scan(&agntID); err != nil {
		t.Fatalf("creating project AGNT: %v", err)
	}
	coreID := f.projectID

	coreToken := insertProjectToken(t, db, f.teamID, coreID, "core")
	agntToken := insertProjectToken(t, db, f.teamID, agntID, "agnt")

	c := cache.NewMemory(time.Hour, time.Hour)
	issues := issuestore.New(database.New(db), db, c)
	positions := wakestore.New(database.New(db))

	hasWork := func(who string, teamID, projectID, tokenID uuid.UUID) bool {
		t.Helper()
		pos, err := positions.Position(ctx, teamID, projectID, tokenID)
		if err != nil {
			t.Fatalf("position for %s: %v", who, err)
		}
		return pos.Head > pos.Cursor
	}
	subject := uuid.New()

	// AGNT opens a question to CORE: the event is addressed to CORE, the party that must answer.
	if err := issues.WithTx(ctx, func(tx issuestore.Store) error {
		return tx.AppendEvent(ctx, issuestore.Event{
			TeamID:          f.teamID,
			ProjectID:       coreID,
			ActorProjectID:  agntID,
			NotifyProjectID: coreID,
			Kind:            issuestore.KindIssueOpened,
			SubjectID:       subject,
		})
	}); err != nil {
		t.Fatalf("appending the opening event: %v", err)
	}

	// CORE has a question to answer; AGNT is only waiting, so a question addressed to CORE is no work
	// for AGNT.
	if !hasWork("CORE", f.teamID, coreID, coreToken) {
		t.Fatal("CORE was not woken for a question addressed to it")
	}
	if hasWork("AGNT", f.teamID, agntID, agntToken) {
		t.Fatal("AGNT was woken for a question addressed to CORE — a sibling's event leaked across")
	}

	// CORE reads its inbox: its cursor catches up to its head, exactly as check_inbox advances it.
	pos, err := positions.Position(ctx, f.teamID, coreID, coreToken)
	if err != nil {
		t.Fatalf("core position before advancing: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO token_cursors (token_id, last_event_id) VALUES ($1, $2)
		 ON CONFLICT (token_id) DO UPDATE SET last_event_id = EXCLUDED.last_event_id`,
		coreToken, pos.Head,
	); err != nil {
		t.Fatalf("advancing CORE cursor: %v", err)
	}
	if hasWork("CORE", f.teamID, coreID, coreToken) {
		t.Fatal("CORE reported work right after reading its inbox, before anyone wrote anything")
	}

	// CORE answers: the event is addressed to AGNT, the other party. This is CORE's OWN write.
	if err := issues.WithTx(ctx, func(tx issuestore.Store) error {
		return tx.AppendEvent(ctx, issuestore.Event{
			TeamID:          f.teamID,
			ProjectID:       coreID,
			ActorProjectID:  coreID,
			NotifyProjectID: agntID,
			Kind:            issuestore.KindIssueAnswered,
			SubjectID:       subject,
		})
	}); err != nil {
		t.Fatalf("appending the answer event: %v", err)
	}

	// THE REGRESSION. With a team-wide head, CORE's own answer sat above CORE's cursor and the next
	// probe reported work — the empty second wake. With a per-project head the answer lifted AGNT's
	// head, not CORE's, so CORE has nothing.
	if hasWork("CORE", f.teamID, coreID, coreToken) {
		t.Fatal("CORE was woken by its own answer — the empty second wake FLWL-82 fixes")
	}

	// And the answer is not lost: it is waiting for AGNT.
	if !hasWork("AGNT", f.teamID, agntID, agntToken) {
		t.Fatal("AGNT was not woken for the answer addressed to it")
	}
}

// TestEventWithNullNotifyWakesEveryone covers the expand/contract seam (000015).
//
// The hosted engine is pinned per image and can lag the schema (D29): an engine that predates
// notify_project_id writes the column NULL. Such an event carries no target, so the probe must treat
// it as addressed to everyone rather than drop it — a missed wake leaves a real answer unseen, far
// worse than one empty probe. This inserts the row the way an old engine would (raw, no notify) and
// checks a fresh token still sees it.
func TestEventWithNullNotifyWakesEveryone(t *testing.T) {
	db, f := newFixture(t)
	ctx := context.Background()

	positions := wakestore.New(database.New(db))
	token := insertProjectToken(t, db, f.teamID, f.projectID, "core")

	// An engine that predates the column: it inserts an event with no notify_project_id.
	if _, err := db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $2, 'issue.opened', 'issue', $3)`,
		f.teamID, f.projectID, uuid.New(),
	); err != nil {
		t.Fatalf("inserting a pre-column event: %v", err)
	}

	pos, err := positions.Position(ctx, f.teamID, f.projectID, token)
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if pos.Head <= pos.Cursor {
		t.Fatal("a NULL-notify event was invisible to the probe — an old engine's write would never wake anyone")
	}
}

// TestProbeSuggestsThePendingEffort is the acceptance of FLWL-84's server half: when a probe finds
// work, it reports the highest rigour tier among that work, so the waker launches a matching model.
//
// It drives the real chain — an open issue carrying a tier, an event that makes the probe say "yes",
// then the probe reading the tier back — and checks both a declared tier and the unspecified default.
// The clamp to the receiver's ceiling is a pure function proved in internal/pkg/effort; here the
// concern is only that the tier travels from the issue row to ProbeResult.SuggestedEffort.
func TestProbeSuggestsThePendingEffort(t *testing.T) {
	cases := []struct {
		name   string
		stored any    // the effort column value: a tier string, or nil for "unspecified"
		expect string // the tier the probe should suggest
	}{
		{"a declared max tier is suggested verbatim", "max", "max"},
		{"a declared low tier is suggested verbatim", "low", "low"},
		{"an unspecified tier falls to standard", nil, "standard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, f := newFixture(t)
			ctx := context.Background()

			// A sibling author, AGNT, opens a question to CORE (f.projectID): the issue is CORE's to
			// answer, so it is CORE's pending work.
			var agntID uuid.UUID
			if err := db.QueryRow(
				"INSERT INTO projects (team_id, key, name) VALUES ($1, 'AGNT', 'Project AGNT') RETURNING id",
				f.teamID,
			).Scan(&agntID); err != nil {
				t.Fatalf("creating project AGNT: %v", err)
			}
			coreID := f.projectID

			var issueID uuid.UUID
			if err := db.QueryRow(
				`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, effort)
				 VALUES ($1, $2, $3, 1, 'a question', 'open', $4) RETURNING id`,
				f.teamID, coreID, agntID, tc.stored,
			).Scan(&issueID); err != nil {
				t.Fatalf("inserting the open issue: %v", err)
			}
			// The event NAMES the issue: the confirming read joins the two, so the subject must be the
			// issue's id — a bare event with no backing issue is exactly what must NOT wake (FLWL-85).
			if _, err := db.Exec(
				`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
				 VALUES ($1, $2, $3, $2, 'issue.opened', 'issue', $4)`,
				f.teamID, coreID, agntID, issueID,
			); err != nil {
				t.Fatalf("inserting the opening event: %v", err)
			}

			c := cache.NewMemory(time.Hour, time.Hour)
			wsvc := wakeservice.New(wakestore.New(database.New(db)), c)
			token := insertProjectToken(t, db, f.teamID, coreID, "core")

			r, err := wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: token})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if !r.HasWork {
				t.Fatal("the probe found no work for an open issue addressed to CORE")
			}
			if r.SuggestedEffort != tc.expect {
				t.Errorf("SuggestedEffort = %q, want %q", r.SuggestedEffort, tc.expect)
			}
		})
	}
}

// TestProbeDoesNotWakeOnNonActionableMovement is the regression for the grave fault of FLWL-85: the
// waker booted full sessions into the void because head > cursor fired on events that were not work.
//
// Each case moves the journal past a fresh token's cursor (so the cheap gate passes) with an event
// that is NOT actionable, and asserts the probe still says HasWork=false — no launch. Before the
// confirming read existed, every one of these woke a full Opus session to find nothing.
func TestProbeDoesNotWakeOnNonActionableMovement(t *testing.T) {
	t.Run("an event with no backing issue (a sibling's traffic on a team-wide head)", func(t *testing.T) {
		db, f := newFixture(t)
		ctx := context.Background()

		// A raw event addressed to this project whose subject issue does not exist for it — the shape a
		// team-wide head produced when a SIBLING wrote, waking everyone.
		if _, err := db.Exec(
			`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
			 VALUES ($1, $2, $2, $2, 'issue.opened', 'issue', $3)`,
			f.teamID, f.projectID, uuid.New(),
		); err != nil {
			t.Fatalf("inserting the orphan event: %v", err)
		}
		assertNoWakeButMoved(t, ctx, db, f.teamID, f.projectID)
	})

	t.Run("a closed issue's event", func(t *testing.T) {
		db, f := newFixture(t)
		ctx := context.Background()

		var siblingID uuid.UUID
		if err := db.QueryRow(
			"INSERT INTO projects (team_id, key, name) VALUES ($1, 'SIBL', 'Project SIBL') RETURNING id",
			f.teamID,
		).Scan(&siblingID); err != nil {
			t.Fatalf("creating the sibling project: %v", err)
		}
		// A CLOSED issue this project authored, and the closing event addressed to it. There is nothing
		// to do on a closed issue: the movement is real, the work is not.
		var closedIssue uuid.UUID
		if err := db.QueryRow(
			`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, closed_at)
			 VALUES ($1, $2, $3, 1, 'a settled question', 'closed', now()) RETURNING id`,
			f.teamID, siblingID, f.projectID,
		).Scan(&closedIssue); err != nil {
			t.Fatalf("inserting the closed issue: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
			 VALUES ($1, $2, $3, $4, 'issue.closed', 'issue', $5)`,
			f.teamID, siblingID, siblingID, f.projectID, closedIssue,
		); err != nil {
			t.Fatalf("inserting the closing event: %v", err)
		}
		assertNoWakeButMoved(t, ctx, db, f.teamID, f.projectID)
	})
}

// assertNoWakeButMoved checks the two halves of the FLWL-85 guarantee at once: the journal HAS moved
// past a fresh token's cursor (the cheap gate would fire), yet the probe reports no work because the
// movement is not actionable. A fresh token sidesteps the escalation ladder's throttle.
func assertNoWakeButMoved(t *testing.T, ctx context.Context, db *sql.DB, teamID, projectID uuid.UUID) {
	t.Helper()
	positions := wakestore.New(database.New(db))
	token := insertProjectToken(t, db, teamID, projectID, "core")

	pos, err := positions.Position(ctx, teamID, projectID, token)
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if pos.Head <= pos.Cursor {
		t.Fatal("the journal did not move — the test is not exercising the gate it means to")
	}

	wsvc := wakeservice.New(positions, cache.NewMemory(time.Hour, time.Hour))
	r, err := wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: teamID, ProjectID: projectID, TokenID: token})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if r.HasWork {
		t.Fatal("the probe reported work for a non-actionable event — a session would boot into the void")
	}
}

// TestProbeWakesForSeenButUnansweredIssue is the regression for FLWL-86, the exact inverse of the
// FLWL-85 fault above: the waker must relaunch for an open issue an agent LOOKED at without answering.
//
// The reported incident: an incoming issue sat open ~10 min on a session Maxence had launched, and the
// waker never relaunched an agent for it. The cause was that the wake decision rode the token's read
// cursor — which check_inbox advances on a mere read — so once the session read the inbox, the issue's
// event sat at or below the cursor and the gate `head > cursor` went false forever. The fix anchors the
// gate on a per-project WAKE WATERMARK the probe alone advances, decoupled from the read cursor.
//
// The scenario, driven through the real chain:
//   - AGNT opens a question to CORE   → an open issue addressed to CORE, with its naming event
//   - CORE's token reads its inbox    → its cursor catches the head (the "looked but did not answer" step)
//   - CORE's probe                    → STILL HasWork=true (the fix; the old cursor gate said false here)
//   - a second probe, fresh token     → HasWork=false: the watermark advanced, so the same standing
//                                        work does not relaunch every probe — the void-loop stays closed
func TestProbeWakesForSeenButUnansweredIssue(t *testing.T) {
	db, f := newFixture(t)
	ctx := context.Background()

	// AGNT, the author; CORE (f.projectID), the recipient that owes the answer.
	var agntID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'AGNT', 'Project AGNT') RETURNING id",
		f.teamID,
	).Scan(&agntID); err != nil {
		t.Fatalf("creating project AGNT: %v", err)
	}
	coreID := f.projectID

	// An open question addressed to CORE, and the event that names it — the shape a real create_issue
	// leaves behind, and what the confirming read joins against.
	var issueID uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state)
		 VALUES ($1, $2, $3, 1, 'a question I have not answered', 'open') RETURNING id`,
		f.teamID, coreID, agntID,
	).Scan(&issueID); err != nil {
		t.Fatalf("inserting the open issue: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $3, $2, 'issue.opened', 'issue', $4)`,
		f.teamID, coreID, agntID, issueID,
	); err != nil {
		t.Fatalf("inserting the opening event: %v", err)
	}

	c := cache.NewMemory(time.Hour, time.Hour)
	positions := wakestore.New(database.New(db))
	wsvc := wakeservice.New(positions, c)
	coreToken := insertProjectToken(t, db, f.teamID, coreID, "core")

	// CORE's session reads its inbox without answering: its cursor catches its head, exactly as
	// check_inbox advances it. This is the step that used to make the issue unwakeable.
	pos, err := positions.Position(ctx, f.teamID, coreID, coreToken)
	if err != nil {
		t.Fatalf("position before advancing: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO token_cursors (token_id, last_event_id) VALUES ($1, $2)
		 ON CONFLICT (token_id) DO UPDATE SET last_event_id = EXCLUDED.last_event_id`,
		coreToken, pos.Head,
	); err != nil {
		t.Fatalf("advancing CORE cursor: %v", err)
	}
	after, err := positions.Position(ctx, f.teamID, coreID, coreToken)
	if err != nil {
		t.Fatalf("position after advancing: %v", err)
	}
	if after.Head != after.Cursor {
		t.Fatalf("cursor did not reach head (%d vs %d) — the test is not exercising the seen-issue gate", after.Cursor, after.Head)
	}

	// THE FIX. The cursor is at the head, so the old `head > cursor` gate would say "no work". The
	// watermark gate still wakes: the open issue is unanswered work the probe has not decided on.
	r, err := wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: coreToken})
	if err != nil {
		t.Fatalf("probe of the seen-but-unanswered issue: %v", err)
	}
	if !r.HasWork {
		t.Fatal("the probe did not wake for an open issue the agent had looked at without answering — FLWL-86 regressed")
	}

	// NO VOID-LOOP. The first probe advanced the watermark to the head it decided on, so a second probe
	// — a fresh token to sidestep the escalation ladder, sharing the per-project watermark — finds
	// nothing new and launches nothing. Only a new event would lift the head above the watermark again.
	fresh := insertProjectToken(t, db, f.teamID, coreID, "core2")
	r, err = wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: fresh})
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	if r.HasWork {
		t.Fatal("the same standing issue woke a second time with no new event — the FLWL-85 void-loop reopened")
	}
}

// TestProbeSuppressionSurvivesAColdCache is the regression for FLWL-90: the re-wake suppression must
// outlive the process. The watermark lived only in the in-process cache, so a hosted engine that
// Render spins down between polls (or a fresh replica) cold-started with watermark 0 and re-decided
// ALL standing work on every poll — re-waking a repo for the same open issue forever. Persisting the
// watermark (000017) fixes it: a cold cache reads the last decided head from Postgres, not 0.
//
// The scenario:
//   - AGNT opens a question to CORE   → an open issue addressed to CORE, unanswered standing work
//   - a first probe                   → HasWork=true, and it persists the watermark it decided on
//   - the cache is DROPPED (a restart / spin-down): a brand-new, cold cache
//   - a second probe, fresh token     → HasWork=false: the durable watermark is read back, not
//                                        defaulted to 0 — the void-loop stays closed across a restart
//   - a NEW event on the issue        → HasWork=true again from a cold cache: suppression covers only
//                                        what was already decided, never new work
func TestProbeSuppressionSurvivesAColdCache(t *testing.T) {
	db, f := newFixture(t)
	ctx := context.Background()

	var agntID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'AGNT', 'Project AGNT') RETURNING id",
		f.teamID,
	).Scan(&agntID); err != nil {
		t.Fatalf("creating project AGNT: %v", err)
	}
	coreID := f.projectID

	// An open question addressed to CORE, and the event that names it: unanswered standing work, the
	// exact shape that re-woke CORE on every cold poll before FLWL-90.
	var issueID uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state)
		 VALUES ($1, $2, $3, 1, 'a standing unanswered question', 'open') RETURNING id`,
		f.teamID, coreID, agntID,
	).Scan(&issueID); err != nil {
		t.Fatalf("inserting the open issue: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $3, $2, 'issue.opened', 'issue', $4)`,
		f.teamID, coreID, agntID, issueID,
	); err != nil {
		t.Fatalf("inserting the opening event: %v", err)
	}

	positions := wakestore.New(database.New(db))

	// First probe, its own cache: it finds the open issue and persists the watermark it decided on.
	svc1 := wakeservice.New(positions, cache.NewMemory(time.Hour, time.Hour))
	r, err := svc1.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: insertProjectToken(t, db, f.teamID, coreID, "core")})
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if !r.HasWork {
		t.Fatal("the first probe did not wake for an open issue addressed to CORE")
	}

	// THE RESTART. A brand-new cold cache, exactly as a spun-down engine or a fresh replica starts, and
	// a fresh token so the escalation ladder cannot be what silences this probe. Before FLWL-90 the
	// cold watermark read 0 and re-decided the still-open issue → HasWork=true, the perpetual wake.
	svc2 := wakeservice.New(positions, cache.NewMemory(time.Hour, time.Hour))
	r, err = svc2.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: insertProjectToken(t, db, f.teamID, coreID, "core2")})
	if err != nil {
		t.Fatalf("second probe after a cold restart: %v", err)
	}
	if r.HasWork {
		t.Fatal("a cold-started probe re-woke for the same standing issue — FLWL-90 regressed, the watermark is not durable")
	}

	// A genuinely NEW event still wakes from a cold cache: the durable watermark suppresses only what
	// was already decided, never new work. AGNT posts a follow-up; its event id is above the watermark.
	if _, err := db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $3, $2, 'issue.message', 'issue', $4)`,
		f.teamID, coreID, agntID, issueID,
	); err != nil {
		t.Fatalf("inserting the follow-up event: %v", err)
	}
	svc3 := wakeservice.New(positions, cache.NewMemory(time.Hour, time.Hour))
	r, err = svc3.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, ProjectID: coreID, TokenID: insertProjectToken(t, db, f.teamID, coreID, "core3")})
	if err != nil {
		t.Fatalf("third probe: %v", err)
	}
	if !r.HasWork {
		t.Fatal("a new event did not lift the head above the durable watermark — the fix over-suppresses")
	}
}

// insertProjectToken creates a project-scoped token and returns its id. The prefix is unique per call
// so two tokens in one test never collide on the prefix unique index.
func insertProjectToken(t *testing.T, db *sql.DB, teamID, projectID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	prefix := strings.ToLower(uuid.NewString()[:8]) + "tkn0"
	if err := db.QueryRow(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope)
		 VALUES ($1, $2, $3, $4, 'test-hash', 'project') RETURNING id`,
		teamID, projectID, name, prefix,
	).Scan(&id); err != nil {
		t.Fatalf("creating token %q: %v", name, err)
	}
	return id
}
