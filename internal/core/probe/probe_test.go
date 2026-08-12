package probe_test

import (
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

func newCache() cache.Cache { return cache.NewMemory(time.Hour, time.Hour) }

// A nil cache degrades to no-signal, never a panic. A hand-built ModuleConfig with no Cache is a
// real construction path (integration test doubles), and a probe helper must survive it.
func TestNilCacheIsSafe(t *testing.T) {
	team, project, token := uuid.New(), uuid.New(), uuid.New()
	probe.RecordEvent(nil, team, project, 1)
	probe.RecordCursor(nil, token, 1)
	probe.Seed(nil, team, project, token, 1, 1)
	if _, ok := probe.Head(nil, team, project); ok {
		t.Error("Head(nil) reported present")
	}
	if _, ok := probe.Cursor(nil, token); ok {
		t.Error("Cursor(nil) reported present")
	}
}

// A cold cache knows neither head nor cursor: the probe must fall through to its cold read, and
// these two absences are how it learns it has to.
func TestColdCacheReportsMissing(t *testing.T) {
	c := newCache()
	if _, ok := probe.Head(c, uuid.New(), uuid.New()); ok {
		t.Error("Head on a cold cache reported present")
	}
	if _, ok := probe.Cursor(c, uuid.New()); ok {
		t.Error("Cursor on a cold cache reported present")
	}
}

// RecordEvent keeps the head monotonic: a lower id — an out-of-order or rolled-back write — must
// never drag it below a position a token may already have read.
func TestRecordEventIsMonotonic(t *testing.T) {
	c := newCache()
	team, project := uuid.New(), uuid.New()

	probe.RecordEvent(c, team, project, 10)
	if h, _ := probe.Head(c, team, project); h != 10 {
		t.Fatalf("head = %d, want 10", h)
	}

	probe.RecordEvent(c, team, project, 5) // lower: ignored
	if h, _ := probe.Head(c, team, project); h != 10 {
		t.Errorf("head = %d after a lower bump, want 10 — the head went backwards", h)
	}

	probe.RecordEvent(c, team, project, 12) // higher: taken
	if h, _ := probe.Head(c, team, project); h != 12 {
		t.Errorf("head = %d after a higher bump, want 12", h)
	}
}

// Two teams, two tokens: a signal for one is never read as a signal for another. The whole cost
// model rests on the scope of these keys.
func TestSignalsAreScoped(t *testing.T) {
	c := newCache()
	teamA, teamB := uuid.New(), uuid.New()
	projA, projB := uuid.New(), uuid.New()
	tokA, tokB := uuid.New(), uuid.New()

	probe.RecordEvent(c, teamA, projA, 7)
	probe.RecordCursor(c, tokA, 3)

	if _, ok := probe.Head(c, teamB, projB); ok {
		t.Error("team B read team A's head")
	}
	if _, ok := probe.Cursor(c, tokB); ok {
		t.Error("token B read token A's cursor")
	}
	if h, _ := probe.Head(c, teamA, projA); h != 7 {
		t.Errorf("team A head = %d, want 7", h)
	}
	if cur, _ := probe.Cursor(c, tokA); cur != 3 {
		t.Errorf("token A cursor = %d, want 3", cur)
	}
}

// The head is per-project inside one team: an event addressed to one project never lifts a sibling's
// head. This is the whole fix — a repo answering an issue writes an event addressed to the other
// party, and its own head must stay put so the probe does not wake it for its own write.
func TestHeadIsPerProjectWithinTeam(t *testing.T) {
	c := newCache()
	team := uuid.New()
	projA, projB := uuid.New(), uuid.New()

	probe.RecordEvent(c, team, projA, 9)

	if _, ok := probe.Head(c, team, projB); ok {
		t.Error("project B read an event addressed to project A — a repo would wake for a sibling's event")
	}
	if h, ok := probe.Head(c, team, projA); !ok || h != 9 {
		t.Errorf("project A head = %d (warm=%v), want 9", h, ok)
	}
}

// Seed warms both scalars at once, the way a cold probe does. After it, head and cursor are the two
// integers the probe compares.
func TestSeedWarmsBoth(t *testing.T) {
	c := newCache()
	team, project, tok := uuid.New(), uuid.New(), uuid.New()

	probe.Seed(c, team, project, tok, 20, 20)

	h, hOK := probe.Head(c, team, project)
	cur, cOK := probe.Cursor(c, tok)
	if !hOK || !cOK {
		t.Fatalf("Seed left a signal cold: head warm=%v cursor warm=%v", hOK, cOK)
	}
	if h != 20 || cur != 20 {
		t.Errorf("head=%d cursor=%d, want 20/20", h, cur)
	}
	if h > cur {
		t.Error("head > cursor right after Seed of equal values — the probe would report phantom work")
	}
}
