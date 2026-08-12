package probe

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | RecordEvent  | Bumps the cached head of the notified project to a new event id   | 65    |
// | Head         | Reads the cached relevance head of a project                      | 80    |
// | RecordCursor | Stores the read position a token reached                          | 93    |
// | Cursor       | Reads the cached read position of a token                         | 101   |
// | Seed         | Stores head and cursor together from one cold read                | 115   |
// | headKey      | Composes the cache key of a project relevance head                | 121   |
// | cursorKey    | Composes the cache key of a token cursor                          | 126   |
//
// Fin du sommaire.
// =====================================================================
//
// The probe signal is the memory half of the wake cost model (D55, docs/DESIGN-WAKE.md §3): asking
// "is there anything for me?" must be an integer-vs-integer compare held in memory, NEVER a query.
// Two scalars carry it, both scoped and both process-local:
//
//   - relevance head : max(events.id) ADDRESSED to a project (its notify_project_id), written by
//     whoever writes the event — never team-wide activity;
//   - token cursor   : the read position, written by check_inbox when it advances.
//
// The head is per-project on purpose. A team-wide head woke a repo for events it authored itself: a
// repo answering an issue bumped the shared head above its own cursor and the next probe woke it for
// nothing. Keying the head by the project the event is meant to wake (the same party the push
// transport signals) means a repo's own writes, addressed to the OTHER party, never lift its head.
//
// The probe (internal/feature/wake) compares head > cursor. Everything here is a thin, typed cover
// over the shared cache.Cache already in ModuleConfig — no new field on a critical struct, one place
// that knows the key format.
//
// WHY BEST-EFFORT IS ENOUGH. RecordEvent is a read-modify-write with no lock, and it is called from
// inside the writer's transaction. Two consequences, both benign, both in the spirit of the inbox's
// own "worst case a stray new flag":
//
//   - A rolled-back transaction may have bumped the head for an event that never committed. The
//     probe then says "something", the woken agent runs check_inbox against the REAL max, finds
//     nothing new, and its cursor catches up. A wasted wake, never a wrong state.
//   - Two concurrent bumps can lose one update. The last writer still stores a real id strictly
//     above the cursor, so the probe still fires; check_inbox reads the true max. A head slightly
//     behind the truth costs a later wake, never a missed one.

import (
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// ttl keeps a signal warm far longer than the cache's default TTL: an entry that expired every few
// minutes would send the next probe back to the database, defeating the zero-SQL property. On
// expiry the probe reseeds from one cold read, so a long TTL only widens the zero-SQL window.
const ttl = 24 * time.Hour

// RecordEvent bumps the cached head of the project the event is addressed to (notifyProjectID) to at
// least eventID. Called by whoever appends to the journal, so that a probe learns a sibling answered
// without a query. The notify target is the party the write means to wake — never the actor — so a
// repo's own writes leave its own head untouched.
//
// The max keeps the head monotonic: an out-of-order or rolled-back write can never drag it below a
// position a token has already read.
func RecordEvent(c cache.Cache, teamID, notifyProjectID uuid.UUID, eventID int64) {
	if c == nil {
		return
	}
	key := headKey(teamID, notifyProjectID)
	if cur, ok := c.Get(key); ok {
		if h, isInt := cur.(int64); isInt && h >= eventID {
			return
		}
	}
	c.Set(key, eventID, ttl)
}

// Head reads the cached relevance head of a project — the id of the latest event addressed to it —
// and whether it was warm.
func Head(c cache.Cache, teamID, projectID uuid.UUID) (int64, bool) {
	if c == nil {
		return 0, false
	}
	v, ok := c.Get(headKey(teamID, projectID))
	if !ok {
		return 0, false
	}
	h, isInt := v.(int64)
	return h, isInt
}

// RecordCursor stores the read position a token reached, so a probe compares against it in memory.
func RecordCursor(c cache.Cache, tokenID uuid.UUID, cursor int64) {
	if c == nil {
		return
	}
	c.Set(cursorKey(tokenID), cursor, ttl)
}

// Cursor reads the cached read position of a token, and whether it was warm.
func Cursor(c cache.Cache, tokenID uuid.UUID) (int64, bool) {
	if c == nil {
		return 0, false
	}
	v, ok := c.Get(cursorKey(tokenID))
	if !ok {
		return 0, false
	}
	cur, isInt := v.(int64)
	return cur, isInt
}

// Seed stores head and cursor together, the way a cold probe warms both from its single read. The
// head belongs to the project the cursor's token speaks for: both come out of the one cold read.
func Seed(c cache.Cache, teamID, projectID, tokenID uuid.UUID, head, cursor int64) {
	RecordEvent(c, teamID, projectID, head)
	RecordCursor(c, tokenID, cursor)
}

// headKey composes the cache key of a project relevance head.
func headKey(teamID, projectID uuid.UUID) string {
	return "probe:head:" + teamID.String() + ":" + projectID.String()
}

// cursorKey composes the cache key of a token cursor.
func cursorKey(tokenID uuid.UUID) string { return "probe:cursor:" + tokenID.String() }
