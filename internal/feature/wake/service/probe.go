package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Probe     | Gates on movement past the watermark, confirms work, dictates cadence | 53 |
// | service.head      | The project relevance head, from memory or one cold read             | 108 |
// | service.watermark | The wake watermark, from memory or the durable store on a cold cache | 126 |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"
	"math"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/google/uuid"
)

// Probe reports whether there is work worth launching a session for, and dictates when the caller may
// probe again.
//
// TWO STEPS, and the split is the whole cost model (D55, FLWL-85):
//
//  1. The cheap gate — has the journal moved past the WAKE WATERMARK? When head and watermark are
//     warm in the cache this is one integer comparison and NO query, so an idle repo is polled
//     forever for free. A cold cache falls through to one read that seeds the head, then goes quiet.
//  2. Only when the gate passes — is that movement ACTUALLY new actionable work? A moved journal
//     means "an event I have not decided on exists", not "a question awaits an answer": a closed
//     issue, or (on a team-wide head) a sibling's traffic, moves the journal without being work for
//     me. A wake is a full session boot, so the probe confirms with one indexed read before it says
//     yes. It returns HasWork=false for non-actionable movement, and the waker launches nothing.
//
// THE BOUNDARY IS THE WATERMARK, NOT THE CURSOR (FLWL-86). Gating on the token's read cursor was the
// exact inverse of the FLWL-85 fault: check_inbox advances the cursor on a mere read, so once any
// session — manual or woken — LOOKED at an open issue without answering it, the cursor sat at the head
// and the issue never woke anyone again. The watermark advances only here, when the probe decides on
// a launch, so a looked-at-but-unanswered issue still wakes. It is advanced on the has-work path so
// the SAME standing work does not relaunch every probe (the void-loop FLWL-85 closed): a new event
// lifts the head above the watermark and earns a fresh wake.
//
// The confirming read runs ONLY on the gate-passed path, never on the idle poll, so the zero-SQL
// steady state holds. After a bump the watermark advances to the evaluated head, so a non-actionable
// event is read once, not on every probe — the steady state settles faster than the cursor gate did.
//
// THE CADENCE IS THE SERVER'S. Before answering, the probe checks the escalation ladder: a client
// that comes back before the interval it was last told is throttled — the handler turns that into a
// 429 — and no work is even looked at. Otherwise the ladder advances (climb when there is no work,
// snap to rung 0 on real work) and the interval it now dictates rides back in NextProbeAfter.
func (s *service) Probe(ctx context.Context, in ProbeInput) (ProbeResult, error) {
	if in.TeamID == uuid.Nil || in.ProjectID == uuid.Nil || in.TokenID == uuid.Nil {
		return ProbeResult{}, fmt.Errorf("%w: incomplete probe scope", ErrInvalidInput)
	}

	now := s.now()
	pace := loadPacing(s.cache, in.TokenID)
	if now.Before(pace.NextAllowed) {
		// The client ignored the cadence: refuse without touching the work path.
		retry := int(math.Ceil(pace.NextAllowed.Sub(now).Seconds()))
		return ProbeResult{Throttled: true, NextProbeAfter: retry}, nil
	}

	head, err := s.head(ctx, in)
	if err != nil {
		return ProbeResult{}, err
	}
	watermark, err := s.watermark(ctx, in)
	if err != nil {
		return ProbeResult{}, err
	}

	// Step 2: confirm the movement is actionable before it becomes a launch, measuring "new" from the
	// watermark. On a clean read — actionable or not — advance the watermark to the head just decided
	// on, so the same standing work is not relaunched every probe. On a read error, do NOT advance and
	// fall back to the bare movement: a transient DB blip must not swallow a real answer, and the
	// daemon's circuit-breaker bounds the retry burst if the error persists.
	hasWork := head > watermark
	var tier string
	if hasWork {
		if actionable, effort, aerr := s.store.Actionable(ctx, in.TeamID, in.ProjectID, watermark); aerr == nil {
			hasWork = actionable
			tier = effort
			probe.RecordWake(s.cache, in.TeamID, in.ProjectID, head)
			// Persist the decision so a spun-down or restarted engine does not re-decide this standing
			// work (FLWL-90). Best-effort: the in-memory RecordWake above already suppresses re-wake for
			// the life of this process, so a failed write costs at most one re-decision after a cold
			// start — the pre-000017 behaviour, never a lost launch. The launch must not hinge on it.
			_ = s.store.SaveWatermark(ctx, in.TeamID, in.ProjectID, head)
		}
	}

	pace = nextPacing(pace, hasWork, now)
	storePacing(s.cache, in.TokenID, pace)

	result := ProbeResult{HasWork: hasWork, NextProbeAfter: int(intervalOf(pace.Rung).Seconds())}
	if hasWork {
		result.SuggestedEffort = tier
	}
	return result, nil
}

// head answers the project relevance head from memory when it can, and from one cold read otherwise.
// The cold read seeds both probe scalars it touches — the head and the piggyback cursor — so every
// later probe of this project answers from memory, the zero-SQL steady state D55 protects.
func (s *service) head(ctx context.Context, in ProbeInput) (int64, error) {
	if h, warm := probe.Head(s.cache, in.TeamID, in.ProjectID); warm {
		return h, nil
	}
	pos, err := s.store.Position(ctx, in.TeamID, in.ProjectID, in.TokenID)
	if err != nil {
		return 0, fmt.Errorf("wake service: probe: %w", err)
	}
	probe.Seed(s.cache, in.TeamID, in.ProjectID, in.TokenID, pos.Head, pos.Cursor)
	return pos.Head, nil
}

// watermark answers the wake watermark from memory when warm, and from the durable store on a cold
// cache — the head the probe last decided on. Seeding it from Postgres (000017) is what makes the
// FLWL-85/86 re-wake suppression survive a spun-down or restarted engine: a cold miss would otherwise
// read 0 and re-decide ALL standing work on every poll, re-waking a repo for any open or answered
// issue (FLWL-90). The cold read seeds the cache, so later probes answer from memory and the idle
// poll stays zero-SQL.
func (s *service) watermark(ctx context.Context, in ProbeInput) (int64, error) {
	if w, warm := probe.Wake(s.cache, in.TeamID, in.ProjectID); warm {
		return w, nil
	}
	w, err := s.store.Watermark(ctx, in.TeamID, in.ProjectID)
	if err != nil {
		return 0, fmt.Errorf("wake service: probe: %w", err)
	}
	probe.RecordWake(s.cache, in.TeamID, in.ProjectID, w)
	return w, nil
}
