package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Probe   | Answers the compare and dictates the next cadence (429/ladder) | 37    |
// | service.hasWork | The head-vs-cursor compare, from memory when it can            | 62    |
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

// Probe reports whether the team journal has moved past the token's cursor, and dictates when the
// caller may probe again.
//
// The steady-state path is the whole point: when both scalars are warm in the cache, the answer is
// a single integer comparison and NO query is issued — an idle repo can be polled forever for free
// (D55). Only a cold cache — a fresh process, or a signal aged out — falls through to one read that
// seeds both, after which the endpoint goes quiet again.
//
// head > cursor, strictly: after check_inbox the two are equal and there is nothing to report; the
// next event bumps the head above the cursor and the probe fires.
//
// THE CADENCE IS THE SERVER'S. Before answering, the probe checks the escalation ladder: a client
// that comes back before the interval it was last told is throttled — the handler turns that into a
// 429 — and no work is even looked at. Otherwise the ladder advances (climb on empty, snap to rung 0
// on an event) and the interval it now dictates rides back in NextProbeAfter.
func (s *service) Probe(ctx context.Context, in ProbeInput) (ProbeResult, error) {
	if in.TeamID == uuid.Nil || in.TokenID == uuid.Nil {
		return ProbeResult{}, fmt.Errorf("%w: incomplete probe scope", ErrInvalidInput)
	}

	now := s.now()
	pace := loadPacing(s.cache, in.TokenID)
	if now.Before(pace.NextAllowed) {
		// The client ignored the cadence: refuse without touching the work path.
		retry := int(math.Ceil(pace.NextAllowed.Sub(now).Seconds()))
		return ProbeResult{Throttled: true, NextProbeAfter: retry}, nil
	}

	hasWork, err := s.hasWork(ctx, in)
	if err != nil {
		return ProbeResult{}, err
	}

	pace = nextPacing(pace, hasWork, now)
	storePacing(s.cache, in.TokenID, pace)
	return ProbeResult{HasWork: hasWork, NextProbeAfter: int(intervalOf(pace.Rung).Seconds())}, nil
}

// hasWork answers the one comparison, from memory when it can. Cold cache: one read seeds both, so
// every later probe of this token and team is free again.
func (s *service) hasWork(ctx context.Context, in ProbeInput) (bool, error) {
	head, headWarm := probe.Head(s.cache, in.TeamID)
	cursor, cursorWarm := probe.Cursor(s.cache, in.TokenID)
	if headWarm && cursorWarm {
		return head > cursor, nil
	}

	pos, err := s.store.Position(ctx, in.TeamID, in.TokenID)
	if err != nil {
		return false, fmt.Errorf("wake service: probe: %w", err)
	}
	probe.Seed(s.cache, in.TeamID, in.TokenID, pos.Head, pos.Cursor)
	return pos.Head > pos.Cursor, nil
}
