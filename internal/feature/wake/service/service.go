package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                         | Ligne |
// |--------------|---------------------------------------------------------------|-------|
// | Service      | The one question the waker asks: is there anything past my cursor | 43    |
// | service      | Implementation over the probe cache and a cold-read store        | 53    |
// | New          | Creates the wake service                                        | 62    |
// | ProbeInput   | Scope of one probe, entirely taken from the token                | 68    |
// | ProbeResult  | The probe's answer: work or not                                   | 80    |
// | RegisterInput | A waker's registration: scope from the token, callback + secret | 88    |
// | RegisterResult | The lease window handed back to the waker                       | 96    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in probe.go.
//
// WHY A PROBE AND NOT check_inbox. Asking "is there anything?" must not cost what asking "what is
// it?" costs. The probe is an integer-vs-integer compare held in memory — the team journal head
// against the token cursor — and touches Postgres only to seed itself once on a cold cache (D55,
// docs/DESIGN-WAKE.md §3). check_inbox stays the 6-query call, fired only when the probe says yes.
//
// This is what lets a waker sit on a dead agent's behalf without cost following time × agents: an
// empty probe is free, so polling an idle repo forever is free.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/wake/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// ErrInvalidInput signals a probe with no team or no token — a scope the token should always carry.
var ErrInvalidInput = errors.New("wake: invalid input")

// Service answers the question a waker repeats on a sleeping agent's behalf, and records where that
// waker can be pushed to.
type Service interface {
	// Probe compares the team journal head with the token cursor. Free in steady state.
	Probe(ctx context.Context, in ProbeInput) (ProbeResult, error)
	// Register records the local waker's callback and secret under a lease, so the engine can push
	// a wake the instant an event drops. Called again, it refreshes the lease.
	Register(ctx context.Context, in RegisterInput) (RegisterResult, error)
}

// service depends on the probe cache for the steady-state compare and on the store for the one cold
// read that seeds it.
type service struct {
	store store.Store
	cache cache.Cache
	// now is the clock the escalation ladder measures against. Held here so a test can drive the
	// rungs and the 429 without sleeping through real minutes.
	now func() time.Time
}

// New creates the wake service.
func New(st store.Store, c cache.Cache) Service {
	return &service{store: st, cache: c, now: time.Now}
}

// ProbeInput carries the scope of one probe. Both fields come from the token: like check_inbox, the
// call has no agent-supplied parameter.
type ProbeInput struct {
	TeamID  uuid.UUID `json:"-"`
	TokenID uuid.UUID `json:"-"`
}

// ProbeResult is the probe's answer.
//
// HasWork says whether the journal has moved past the cursor; a false answer is the common case and
// the cheap one. NextProbeAfter is the cadence the SERVER dictates: the number of seconds before the
// client may probe again, climbing the escalation ladder as empty probes pile up and snapping back
// to the base on any event (D55, DESIGN-WAKE §3). Throttled marks a probe that came back too soon —
// the client ignored a previous NextProbeAfter — and the handler turns it into a 429.
type ProbeResult struct {
	HasWork       bool `json:"has_work"`
	NextProbeAfter int  `json:"next_probe_after"`
	Throttled     bool `json:"-"`
}

// RegisterInput carries a waker's registration. TeamID and ProjectID come from the token; Callback
// and Secret come from the waker, which owns both.
type RegisterInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Callback  string    `json:"callback"`
	Secret    string    `json:"secret"`
}

// RegisterResult tells the waker how long its lease holds, so it knows how often to refresh.
type RegisterResult struct {
	LeaseSeconds int `json:"lease_seconds"`
}
