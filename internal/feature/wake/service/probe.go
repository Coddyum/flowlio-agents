package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Probe        | Gates on movement, confirms actionable work, dictates cadence | 44 |
// | service.journalMoved | The cheap head-vs-cursor gate, and the cursor it read          | 87 |
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
//  1. The cheap gate — has the journal moved past the cursor? When both scalars are warm in the
//     cache this is one integer comparison and NO query, so an idle repo is polled forever for free.
//     A cold cache falls through to one read that seeds both, then goes quiet again.
//  2. Only when the gate passes — is that movement ACTUALLY new actionable work? `head > cursor`
//     means "an event I have not accounted for exists", not "a question awaits an answer": a closed
//     issue, or (on a team-wide head) a sibling's traffic, moves the journal without being work for
//     me. A wake is a full session boot, so the probe confirms with one indexed read before it says
//     yes. It returns HasWork=false for non-actionable movement, and the waker launches nothing.
//
// The confirming read runs ONLY on the gate-passed path, never on the idle poll, so the zero-SQL
// steady state holds. After a non-actionable bump the ladder climbs (the confirm keeps returning "no
// work"), so the read runs a handful of times then rarely — an occasional query, never a session.
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

	moved, cursor, err := s.journalMoved(ctx, in)
	if err != nil {
		return ProbeResult{}, err
	}

	// Step 2: confirm the movement is actionable before it becomes a launch. On a read error, fall
	// back to the bare movement — a transient DB blip must not swallow a real answer; the daemon's
	// circuit-breaker is the backstop against a burst of empty wakes if that error persists.
	hasWork := moved
	var tier string
	if moved {
		if actionable, effort, aerr := s.store.Actionable(ctx, in.TeamID, in.ProjectID, cursor); aerr == nil {
			hasWork = actionable
			tier = effort
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

// journalMoved answers the cheap gate — head > cursor — from memory when it can, and hands back the
// cursor so the confirming read knows the boundary "new" is measured from. Cold cache: one read seeds
// both scalars, so every later probe of this token and team is free again.
func (s *service) journalMoved(ctx context.Context, in ProbeInput) (bool, int64, error) {
	head, headWarm := probe.Head(s.cache, in.TeamID, in.ProjectID)
	cursor, cursorWarm := probe.Cursor(s.cache, in.TokenID)
	if headWarm && cursorWarm {
		return head > cursor, cursor, nil
	}

	pos, err := s.store.Position(ctx, in.TeamID, in.ProjectID, in.TokenID)
	if err != nil {
		return false, 0, fmt.Errorf("wake service: probe: %w", err)
	}
	probe.Seed(s.cache, in.TeamID, in.ProjectID, in.TokenID, pos.Head, pos.Cursor)
	return pos.Head > pos.Cursor, pos.Cursor, nil
}
