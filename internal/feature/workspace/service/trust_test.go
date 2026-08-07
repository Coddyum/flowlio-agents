package service

// What this file locks down: the only thing the service decides about the graph, that is the
// validation of two strings typed by a human.
//
// Everything else — the projects' membership of the team, the authorisation itself — lives in the
// SQL and is not testable here, deliberately. A service test proving tenancy would prove the
// tenancy OF THE FAKE.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// trustSpy records the keys the store received, as the service normalised them. That is the only
// way to check normalisation really happens BEFORE the call — a fake normalising in turn would make
// the test green whatever the implementation.
type trustSpy struct {
	store.Store

	calls int
	from  string
	to    string
}

func (s *trustSpy) AllowTrust(_ context.Context, _ uuid.UUID, from, to string) (bool, error) {
	s.calls++
	s.from, s.to = from, to
	return true, nil
}

func (s *trustSpy) RevokeTrust(_ context.Context, _ uuid.UUID, from, to string) (bool, error) {
	s.calls++
	s.from, s.to = from, to
	return true, nil
}

// A project cannot trust itself, and the message SAYS so.
//
// The database would refuse anyway (project_trust_not_self, migration 000013), but it would return
// a `not found` — that is, to the human, the same message as if they had typed a key that does not
// exist. This check exists to turn that silence into a useful sentence.
//
// The `frnt`/`FRNT` case is the one that matters: the comparison happens AFTER normalisation.
// Without that, validation would pass and the database would return a 404 on a command whose
// mistake was obvious.
func TestTrustRefusesASelfEdge(t *testing.T) {
	teamID := uuid.New()

	cases := []struct{ name, from, to string }{
		{"identical keys", "FRNT", "FRNT"},
		{"different case", "frnt", "FRNT"},
		{"surrounding spaces", " FRNT ", "FRNT"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for verb, call := range map[string]func(Service, TrustPairInput) error{
				"allow": func(s Service, in TrustPairInput) error { _, err := s.AllowTrust(context.Background(), in); return err },
				"deny": func(s Service, in TrustPairInput) error {
					_, err := s.RevokeTrust(context.Background(), in)
					return err
				},
			} {
				spy := &trustSpy{}
				err := call(New(spy), TrustPairInput{TeamID: teamID, From: c.from, To: c.to})

				if !errors.Is(err, ErrInvalidInput) {
					t.Errorf("%s: error = %v, want ErrInvalidInput", verb, err)
				}
				if !strings.Contains(err.Error(), "itself") {
					t.Errorf("%s: message = %q, want a sentence explaining the refusal", verb, err)
				}
				if spy.calls != 0 {
					t.Errorf("%s: the store was called %d times — the refusal comes after the work", verb, spy.calls)
				}
			}
		})
	}
}

// The keys reach the store in UPPERCASE and without spaces. `frnt` and `FRNT` name the same
// project: letting case decide whether an edge exists would produce two graphs for a single
// intention, one of which nobody ever reads.
//
// MUTATION: removing `strings.ToUpper` from normalisePair makes this test fail.
func TestTrustNormalisesKeysBeforeTheStore(t *testing.T) {
	teamID := uuid.New()

	spy := &trustSpy{}
	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		TeamID: teamID, From: " frnt ", To: "core",
	}); err != nil {
		t.Fatalf("AllowTrust: %v", err)
	}

	if spy.from != "FRNT" || spy.to != "CORE" {
		t.Errorf("the store received (%q, %q), want (\"FRNT\", \"CORE\")", spy.from, spy.to)
	}
}

// The ORDER of the two keys is carried through to the store as is: the service does not sort.
//
// THE RATIONALE CHANGED WITH CARD 11, THE ASSERTION DID NOT. It used to be a note about canonical
// pairs: `least`/`greatest` ran in the query, on the UUIDs, so a sort by key here would have been a
// second canonical order — wrong, but only wasteful. Since migration 000013 the order IS THE
// AUTHORISATION: `ZULU ALFA` says ZULU may question ALFA and says nothing about the other way. A
// service that sorted the two keys would silently turn `allow ZULU ALFA` into `allow ALFA ZULU`,
// and `deny` would cut the edge the human did not name.
//
// The keys are chosen so alphabetical order and command order DISAGREE: with ZULU first, a sort
// would show up here and nowhere else.
func TestTrustDoesNotReorderKeys(t *testing.T) {
	teamID := uuid.New()

	spy := &trustSpy{}
	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		TeamID: teamID, From: "ZULU", To: "ALFA",
	}); err != nil {
		t.Fatalf("AllowTrust: %v", err)
	}

	if spy.from != "ZULU" || spy.to != "ALFA" {
		t.Errorf("the store received (%q, %q), want (\"ZULU\", \"ALFA\") — the order of the "+
			"command IS the direction of the edge, and nothing may reorder it", spy.from, spy.to)
	}
}

// A malformed key is refused before reaching the store, by the same validator as everywhere else.
func TestTrustRejectsMalformedKeys(t *testing.T) {
	teamID := uuid.New()

	for _, key := range []string{"", "F", "frnt-web", "1FRNT", "WAYTOOLONGKEY", "FR NT"} {
		t.Run("key "+key, func(t *testing.T) {
			spy := &trustSpy{}
			if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
				TeamID: teamID, From: key, To: "CORE",
			}); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
			if spy.calls != 0 {
				t.Errorf("the store was called despite an invalid key")
			}
		})
	}
}

// With no resolved team, nothing leaves. This is the wiring guardrail: were a handler to forget
// setting TeamID after teamFor, the write would go out under uuid.Nil — hence under no team, hence
// as a silent `not found` rather than a visible programming error.
func TestTrustRefusesAnUnresolvedTeam(t *testing.T) {
	spy := &trustSpy{}

	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		From: "FRNT", To: "CORE",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("AllowTrust with no team: error = %v, want ErrInvalidInput", err)
	}
	if _, err := New(spy).ListTrust(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ListTrust with no team: error = %v, want ErrInvalidInput", err)
	}
	if spy.calls != 0 {
		t.Errorf("the store was called with no resolved team")
	}
}
