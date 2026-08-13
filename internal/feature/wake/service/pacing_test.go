package service

import (
	"context"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/wake/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// fakeStore stands in for the cold read. Every test below keeps the probe cache warm, so Position is
// never reached — but the service needs a store to build. actionable is what the confirming read
// returns once the journal has moved; the pacing tests drive movement through the cache and keep it
// true, so the ladder sees real work exactly as before.
type fakeStore struct {
	pos        store.Position
	actionable bool
	effort     string
}

func (f fakeStore) Position(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (store.Position, error) {
	return f.pos, nil
}

func (f fakeStore) Actionable(context.Context, uuid.UUID, uuid.UUID, int64) (bool, string, error) {
	return f.actionable, f.effort, nil
}

func (f fakeStore) Watermark(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}

func (f fakeStore) SaveWatermark(context.Context, uuid.UUID, uuid.UUID, int64) error {
	return nil
}

// The ladder climbs one rung every five empty probes and never past the cap; any event snaps it back
// to rung 0. This is the table in DESIGN-WAKE §3, checked rung by rung.
func TestNextPacingClimbsThenCapsThenResets(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	p := pacing{}

	// Five empty probes to leave rung 0.
	for i := 0; i < 4; i++ {
		p = nextPacing(p, false, base)
		if p.Rung != 0 {
			t.Fatalf("after %d empty probes rung = %d, want 0", i+1, p.Rung)
		}
	}
	p = nextPacing(p, false, base)
	if p.Rung != 1 || p.Empties != 0 {
		t.Fatalf("fifth empty probe: rung=%d empties=%d, want 1/0", p.Rung, p.Empties)
	}

	// Climb to the cap: 5 empties per rung, four more rungs to reach rung 5.
	for rung := 1; rung < 5; rung++ {
		for i := 0; i < 5; i++ {
			p = nextPacing(p, false, base)
		}
	}
	if p.Rung != 5 {
		t.Fatalf("rung after climbing = %d, want 5 (the cap)", p.Rung)
	}
	if intervalOf(p.Rung) != 6*time.Hour {
		t.Fatalf("cap interval = %s, want 6h", intervalOf(p.Rung))
	}

	// The cap holds: more empties never promote past it.
	for i := 0; i < 20; i++ {
		p = nextPacing(p, false, base)
	}
	if p.Rung != 5 {
		t.Fatalf("rung past the cap = %d, want 5", p.Rung)
	}

	// An event snaps back to rung 0.
	p = nextPacing(p, true, base)
	if p.Rung != 0 || p.Empties != 0 {
		t.Fatalf("after an event rung=%d empties=%d, want 0/0", p.Rung, p.Empties)
	}
	if !p.NextAllowed.Equal(base.Add(time.Minute)) {
		t.Errorf("NextAllowed after reset = %s, want base+1m", p.NextAllowed)
	}
}

func newService(t *testing.T, clock time.Time) (*service, cache.Cache, func(time.Time)) {
	t.Helper()
	c := cache.NewMemory(time.Hour, time.Hour)
	svc := New(fakeStore{}, c).(*service)
	now := clock
	svc.now = func() time.Time { return now }
	return svc, c, func(at time.Time) { now = at }
}

// A client that comes back before the cadence it was told takes a 429, and no work is even looked
// at. Wait it out, and the probe answers again. This is the 429 the done-when names.
func TestProbeThrottlesTheTooSoonClient(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	svc, c, setNow := newService(t, base)

	_ = c
	team, project, token := uuid.New(), uuid.New(), uuid.New()
	in := ProbeInput{TeamID: team, ProjectID: project, TokenID: token}
	// The fake store reports head == cursor (no work); the first probe cold-reads it and warms the
	// cache, so the throttle path that follows is exercised on a warm compare.

	first, err := svc.Probe(context.Background(), in)
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if first.Throttled {
		t.Fatal("first probe throttled — nothing preceded it")
	}
	if first.NextProbeAfter != 60 {
		t.Fatalf("first next_probe_after = %d, want 60 (rung 0)", first.NextProbeAfter)
	}

	// Same instant: the client ignored the 60s it was handed.
	again, err := svc.Probe(context.Background(), in)
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	if !again.Throttled {
		t.Fatal("a probe repeated within the interval was not throttled")
	}
	if again.NextProbeAfter <= 0 || again.NextProbeAfter > 60 {
		t.Errorf("throttled retry = %d, want (0,60]", again.NextProbeAfter)
	}

	// Wait the interval out: allowed again.
	setNow(base.Add(61 * time.Second))
	third, err := svc.Probe(context.Background(), in)
	if err != nil {
		t.Fatalf("third probe: %v", err)
	}
	if third.Throttled {
		t.Fatal("a probe after the interval was still throttled")
	}
}
