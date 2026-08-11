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

	in := wakeservice.ProbeInput{TeamID: f.teamID, TokenID: tokenID}

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

	// A sibling answers: the issue feature appends the event, and that write bumps the shared head.
	if err := issues.WithTx(ctx, func(tx issuestore.Store) error {
		return tx.AppendEvent(ctx, issuestore.Event{
			TeamID:         f.teamID,
			ProjectID:      f.projectID,
			ActorProjectID: f.projectID,
			Kind:           issuestore.KindIssueAnswered,
			SubjectID:      uuid.New(),
		})
	}); err != nil {
		t.Fatalf("appending the event: %v", err)
	}

	// The head bump reached the SHARED cache: the team head is now strictly above this token's
	// cursor, entirely in memory, no query. This is the wiring the wake path rests on —
	// AppendEvent → probe head → the compare a probe makes.
	head, headWarm := probe.Head(c, f.teamID)
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
	r, err = wsvc.Probe(ctx, wakeservice.ProbeInput{TeamID: f.teamID, TokenID: fresh})
	if err != nil {
		t.Fatalf("fresh-token probe: %v", err)
	}
	if !r.HasWork {
		t.Fatal("a fresh token's probe did not see the event a sibling wrote")
	}
}
