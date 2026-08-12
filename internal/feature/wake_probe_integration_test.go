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

	// A sibling answers, addressing the event to THIS project: the issue feature appends it, and that
	// write bumps this project's relevance head.
	if err := issues.WithTx(ctx, func(tx issuestore.Store) error {
		return tx.AppendEvent(ctx, issuestore.Event{
			TeamID:          f.teamID,
			ProjectID:       f.projectID,
			ActorProjectID:  f.projectID,
			NotifyProjectID: f.projectID,
			Kind:            issuestore.KindIssueAnswered,
			SubjectID:       uuid.New(),
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
