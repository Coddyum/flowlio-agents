package main

import (
	"context"
	"testing"
)

// The plan is the sequencing, and it is data: self-host brings up the database, then the engine,
// then the waker; hosted runs the waker alone (DESIGN-WAKE §4.1). This pins that order without a
// container or a live engine.
func TestUpPlanComposition(t *testing.T) {
	noop := func(context.Context) error { return nil }
	runners := upRunners{dbUp: noop, engine: noop, waker: noop}

	self := upPlan(modeSelfHost, runners)
	gotSelf := stepNames(self)
	wantSelf := []string{"database", "engine", "waker"}
	if !equal(gotSelf, wantSelf) {
		t.Errorf("self-host plan = %v, want %v", gotSelf, wantSelf)
	}

	hosted := upPlan(modeHosted, runners)
	gotHosted := stepNames(hosted)
	wantHosted := []string{"waker"}
	if !equal(gotHosted, wantHosted) {
		t.Errorf("hosted plan = %v, want %v — hosted must never start an engine or a database", gotHosted, wantHosted)
	}
}

func TestDetectMode(t *testing.T) {
	t.Setenv("FLOWLIO_MODE", "hosted")
	if m, err := detectMode(); err != nil || m != modeHosted {
		t.Errorf("FLOWLIO_MODE=hosted → mode=%v err=%v, want hosted", m, err)
	}
	t.Setenv("FLOWLIO_MODE", "")
	if m, err := detectMode(); err != nil || m != modeSelfHost {
		t.Errorf("unset → mode=%v err=%v, want self-host default", m, err)
	}
	t.Setenv("FLOWLIO_MODE", "nonsense")
	if _, err := detectMode(); err == nil {
		t.Error("an unknown FLOWLIO_MODE was accepted")
	}
}

func stepNames(steps []upStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
