package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | pacing      | The per-token ladder state the server keeps between probes          | 54    |
// | intervalOf  | The interval a rung dictates                                       | 62    |
// | nextPacing  | Advances the ladder from one probe's outcome                        | 78    |
// | loadPacing  | Reads a token's ladder state from the cache                         | 93    |
// | storePacing | Writes a token's ladder state back                                 | 107   |
// | paceKey     | Composes the cache key of a token's ladder state                    | 112   |
//
// Fin du sommaire.
// =====================================================================
//
// THE ESCALATION LADDER, server-side (D55, DESIGN-WAKE §3, §11.3). The client does not choose how
// often it probes — the server does, and hands back the interval on every reply. A daemon
// misconfigured to hammer the endpoint cannot cost the day: it comes back before the interval it was
// told, and takes a 429 instead of an answer.
//
// The rungs: 5 empty probes each, promote on empty, snap back to rung 0 on any event.
//
//	rung 0 → 1 min    rung 3 → 15 min
//	rung 1 → 2 min    rung 4 → 1 h
//	rung 2 → 5 min    rung 5 → 6 h (the cap, ∞ probes)
//
// It is the server, not the daemon, that can say "the whole team has been silent for hours, come
// back in six": the server sees the team, the daemon sees only itself.

import (
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// ladderIntervals is the interval each rung dictates. The last is the cap: a token parked there
// stays there until an event resets it.
var ladderIntervals = []time.Duration{
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
}

// probesPerRung is how many empty probes a rung absorbs before the next one.
const probesPerRung = 5

// pacing is the ladder state kept per token between probes. Empties counts consecutive empty probes
// on the current rung; NextAllowed is the earliest instant the client may probe again.
type pacing struct {
	Rung        int
	Empties     int
	NextAllowed time.Time
}

// intervalOf yields the interval a rung dictates, clamped to the cap. An out-of-range rung — which
// nextPacing never produces — still returns the cap rather than panicking.
func intervalOf(rung int) time.Duration {
	if rung < 0 {
		rung = 0
	}
	if rung >= len(ladderIntervals) {
		rung = len(ladderIntervals) - 1
	}
	return ladderIntervals[rung]
}

// nextPacing advances the ladder from one probe's outcome.
//
// An event snaps the whole thing back to rung 0: the daemon has just been told there is work, and
// the next thing after that is not a long wait. An empty probe adds to the rung's tally and, once
// the tally fills, promotes to the next rung — never past the cap. NextAllowed is stamped from the
// interval the new rung dictates, and it is what a too-soon probe is measured against.
func nextPacing(prev pacing, hasWork bool, now time.Time) pacing {
	if hasWork {
		return pacing{Rung: 0, Empties: 0, NextAllowed: now.Add(intervalOf(0))}
	}

	rung, empties := prev.Rung, prev.Empties+1
	if empties >= probesPerRung && rung < len(ladderIntervals)-1 {
		rung++
		empties = 0
	}
	return pacing{Rung: rung, Empties: empties, NextAllowed: now.Add(intervalOf(rung))}
}

// loadPacing reads a token's ladder state, or the zero state for a token that never probed. The zero
// state has a zero NextAllowed, which is before any real instant: a first probe is always allowed.
func loadPacing(c cache.Cache, tokenID uuid.UUID) pacing {
	if v, ok := c.Get(paceKey(tokenID)); ok {
		if p, isPacing := v.(pacing); isPacing {
			return p
		}
	}
	return pacing{}
}

// pacingTTL keeps the ladder state warm past the longest interval, so the cap actually holds instead
// of being forgotten and reset to rung 0 by a cold read.
const pacingTTL = 12 * time.Hour

// storePacing writes a token's ladder state back.
func storePacing(c cache.Cache, tokenID uuid.UUID, p pacing) {
	c.Set(paceKey(tokenID), p, pacingTTL)
}

// paceKey composes the cache key of a token's ladder state.
func paceKey(tokenID uuid.UUID) string { return "wake:pace:" + tokenID.String() }
