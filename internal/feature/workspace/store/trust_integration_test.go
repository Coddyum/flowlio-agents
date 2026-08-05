package store_test

// What this file locks down: the three administration queries of the graph.
//
// The table's CONSTRAINTS are covered by TestDatabaseRejectsIllegalTrustEdges
// (store_integration_test.go); here we check what the queries make OF those constraints —
// idempotence, key resolution, team boundary — that is, what a human sees.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// onlyPair returns a graph's single edge in a form INDEPENDENT OF THE ORDER of the two keys.
//
// The order of `first_key`/`second_key` is that of the UUIDs in the database, not alphabetical
// order nor the order of the command: asserting `SecondKey == "FRNT"` would be a coin flip on every
// run. That is exactly the defect the first version of this file carried, and which only showed up
// when playing the whole suite.
//
// Sorting here weakens nothing: an edge is a PAIR, and claiming to test a direction on a structure
// that has none would be testing a property the product does not promise.
func onlyPair(t *testing.T, edges []store.TrustEdge) string {
	t.Helper()

	if len(edges) != 1 {
		t.Fatalf("%d edges, want exactly 1: %+v", len(edges), edges)
	}
	keys := []string{edges[0].FirstKey, edges[0].SecondKey}
	sort.Strings(keys)
	return strings.Join(keys, "↔")
}

// The nominal cycle: open, replay, read, close, replay.
//
// Both verbs are idempotent and say so — `created` and `removed` tell "done" from "it already was"
// WITHOUT a second round trip. Without those flags, the CLI would have to re-read the graph after
// every write to know what to display.
func TestTrustLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	if _, err := st.CreateProject(ctx, team.ID, "FRNT", "front"); err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}

	t.Run("opening", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust: %v", err)
		}
		if !created {
			t.Error("created = false on the first opening")
		}
	})

	t.Run("replaying the opening", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust (replay): %v", err)
		}
		if created {
			t.Error("created = true on the replay: the command is not idempotent")
		}
	})

	// The edge is a PAIR: declaring it the other way round does not create a second row, and says
	// so. Under a directed table, this case would have returned created = true and the graph would
	// have carried two rows for a single authorisation.
	t.Run("replaying the other way round", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("AllowTrust (reverse direction): %v", err)
		}
		if created {
			t.Error("created = true the other way round: the edge is not symmetric")
		}
	})

	t.Run("read", func(t *testing.T) {
		edges, err := st.ListTrustEdges(ctx, team.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		// A pair declared three times (both directions, one replay) stays ONE row.
		if got := onlyPair(t, edges); got != "CORE↔FRNT" {
			t.Errorf("graph = %s, want CORE↔FRNT", got)
		}
		if edges[0].CreatedAt.IsZero() {
			t.Error("created_at is zero: the declaration date is lost")
		}
	})

	t.Run("closing", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("RevokeTrust: %v", err)
		}
		if !removed {
			t.Error("removed = false although the pair was declared")
		}
	})

	t.Run("replaying the closing", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("RevokeTrust (replay): %v", err)
		}
		if removed {
			t.Error("removed = true on the replay: the command is not idempotent")
		}
	})

	t.Run("the graph is empty", func(t *testing.T) {
		edges, err := st.ListTrustEdges(ctx, team.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		if len(edges) != 0 {
			t.Errorf("%d edges after closing, want 0", len(edges))
		}
	})
}

// A key that does not resolve yields ErrNotFound on BOTH verbs.
//
// This is the accepted departure from docs/DESIGN-TRUST.md, which planned an `:execrows` for
// RevokeTrust: a bare DELETE would have returned "nothing to remove" to a human who just typed a
// key wrong, that is, an apparent success. These routes are ADMIN and an admin already enumerates
// every project of every team: there is no oracle to protect, hence nothing to gain from keeping
// the error quiet.
//
// MUTATION: going back to `DELETE ... USING projects a, projects b` without the `pair` CTE makes
// the "closing, unknown key" subtest fail.
func TestTrustRefusesUnknownKeys(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}

	cases := []struct {
		name  string
		first string
		last  string
	}{
		{"unknown second key", "CORE", "NOPE"},
		{"unknown first key", "NOPE", "CORE"},
		{"both unknown", "NOPE", "NADA"},
		// Case is NOT normalised by the query: it is the service that uppercases. This case pins the
		// boundary — were normalisation to migrate into the SQL, it would turn green and one would
		// have to decide where it lives, rather than having it in both places.
		{"lowercase key, not normalised by the query", "core", "CORE"},
	}

	for _, c := range cases {
		t.Run("opening, "+c.name, func(t *testing.T) {
			if _, err := st.AllowTrust(ctx, team.ID, c.first, c.last); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("AllowTrust(%s, %s): error = %v, want ErrNotFound", c.first, c.last, err)
			}
		})
		t.Run("closing, "+c.name, func(t *testing.T) {
			if _, err := st.RevokeTrust(ctx, team.ID, c.first, c.last); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("RevokeTrust(%s, %s): error = %v, want ErrNotFound", c.first, c.last, err)
			}
		})
	}
}

// The tenancy scope lives in the query: a key that exists IN ANOTHER TEAM is not found, not merely
// forbidden. And a graph never leaks to the neighbour.
//
// MUTATION: removing `a.team_id = @team_id` from any of the three queries makes this test fail.
func TestTrustNeverCrossesTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	mine := createTeam(t, st, db)
	other := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, mine.ID, "CORE", "core at mine"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	if _, err := st.CreateProject(ctx, mine.ID, "FRNT", "front at mine"); err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}
	// The neighbour has a project whose key also exists at mine: this is the case that traps a query
	// resolving by key without a team.
	if _, err := st.CreateProject(ctx, other.ID, "CORE", "core at the neighbour's"); err != nil {
		t.Fatalf("CreateProject CORE (neighbour): %v", err)
	}
	if _, err := st.CreateProject(ctx, other.ID, "OPS", "ops at the neighbour's"); err != nil {
		t.Fatalf("CreateProject OPS: %v", err)
	}

	// From my team, the neighbour's OPS key does not exist.
	if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AllowTrust towards one of the neighbour's projects: error = %v, want ErrNotFound", err)
	}

	// Each one opens at home, without seeing the other.
	if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "FRNT"); err != nil {
		t.Fatalf("AllowTrust at mine: %v", err)
	}
	if _, err := st.AllowTrust(ctx, other.ID, "CORE", "OPS"); err != nil {
		t.Fatalf("AllowTrust at the neighbour's: %v", err)
	}

	mineEdges, err := st.ListTrustEdges(ctx, mine.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (mine): %v", err)
	}
	if got := onlyPair(t, mineEdges); got != "CORE↔FRNT" {
		t.Errorf("my graph = %s, want CORE↔FRNT", got)
	}

	otherEdges, err := st.ListTrustEdges(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (neighbour): %v", err)
	}
	if got := onlyPair(t, otherEdges); got != "CORE↔OPS" {
		t.Errorf("the neighbour's graph = %s, want CORE↔OPS", got)
	}

	// And I cannot close theirs, even by naming their keys exactly.
	if _, err := st.RevokeTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RevokeTrust on the neighbour's pair: error = %v, want ErrNotFound", err)
	}
	stillThere, err := st.ListTrustEdges(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (neighbour, after): %v", err)
	}
	if len(stillThere) != 1 {
		t.Errorf("the neighbour's graph has %d edges after my attempt, want 1", len(stillThere))
	}
}

// Two mutations the first draft of this file let through, found by the milestone's adversarial
// review. Each has its test, and each calls the STORE directly.
//
// SHARED LESSON: the existing tests proved both properties through the SERVICE, which cuts in
// upstream. Proving in the layer that validates, rather than in the one that decides, leaves the
// query free to guarantee nothing.
func TestTrustQueriesGuardTheirOwnScope(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	mine := createTeam(t, st, db)
	other := createTeam(t, st, db)

	for _, p := range []struct {
		team store.Team
		key  string
	}{{mine, "CORE"}, {mine, "FRNT"}, {other, "CORE"}, {other, "OPS"}} {
		if _, err := st.CreateProject(ctx, p.team.ID, p.key, "project "+p.key); err != nil {
			t.Fatalf("CreateProject %s: %v", p.key, err)
		}
	}

	// The neighbour declares its pair. It must be neither readable nor closable from my team.
	if _, err := st.AllowTrust(ctx, other.ID, "CORE", "OPS"); err != nil {
		t.Fatalf("AllowTrust at the neighbour's: %v", err)
	}

	// MUTATION: removing `a.team_id = @team_id` from the `pair` CTE of RevokeTrust.
	//
	// The existing boundary test did not catch it: it passed OPS in SECOND position, and
	// `b.team_id` — still in place — was enough to make the resolution fail. What is needed is a key
	// that exists ONLY at the neighbour's in FIRST position, and a key of my team in second: only
	// the constraint on `a` decides then.
	//
	// Under the mutation, `a` resolves to the neighbour's OPS, `b` to my CORE, the pair resolves,
	// and the query returns `removed=false` with no error — instead of the ErrNotFound a key outside
	// my team must produce.
	t.Run("RevokeTrust scopes both keys, not just the second", func(t *testing.T) {
		if _, err := st.RevokeTrust(ctx, mine.ID, "OPS", "CORE"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound: OPS does not exist in my team", err)
		}
		// And the neighbour's pair is untouched.
		edges, err := st.ListTrustEdges(ctx, other.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		if len(edges) != 1 {
			t.Errorf("the neighbour's graph has %d edges, want 1", len(edges))
		}
	})

	// MUTATION: replacing `a.id <> b.id` with `true` in AllowTrust.
	//
	// The self-pair refusal was only proven by the service, which validates `first != second` BEFORE
	// calling the store. The query has to refuse it too: that is the second turn of the key, and it
	// is what holds if a caller reaches the store directly.
	t.Run("AllowTrust refuses a self-pair in the query", func(t *testing.T) {
		if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "CORE"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound: the query must refuse the self-pair without "+
				"letting the ordering CHECK raise a second error path", err)
		}
		edges, err := st.ListTrustEdges(ctx, mine.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		if len(edges) != 0 {
			t.Errorf("%d edge(s) created by a self-pair", len(edges))
		}
	})
}
