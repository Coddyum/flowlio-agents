package store_test

// What this file locks down: the three administration queries of the graph, now that the edge is
// DIRECTED (migration 000013).
//
// The table's CONSTRAINTS are covered by TestDatabaseRejectsIllegalTrustEdges
// (store_integration_test.go); here we check what the queries make OF those constraints —
// idempotence, key resolution, team boundary, and above all the fact that the two directions of a
// couple are two independent declarations.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// readGraph renders a team's graph as one readable string — "CORE→FRNT, FRNT→CORE" — in the order
// the query returns it, DIRECTION INCLUDED.
//
// It replaces onlyPair, which sorted the two ends of each edge. That was right under a pair —
// first_key and second_key came back in UUID order, which is a coin flip and means nothing — and it
// is exactly wrong now: sorting the ends renders CORE→FRNT and FRNT→CORE identically, so every
// assertion built on it would pass on a graph pointing the other way. The rows are not sorted here
// either, because ListTrustEdges declares `ORDER BY a.key, b.key` and that order is what a human
// reads.
//
// One string rather than a slice, so a failing assertion prints the graph that IS, next to the
// graph that was wanted, rather than a length or a boolean.
func readGraph(t *testing.T, st store.Store, team store.Team) string {
	t.Helper()

	edges, err := st.ListTrustEdges(context.Background(), team.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges: %v", err)
	}

	rendered := make([]string, 0, len(edges))
	for _, e := range edges {
		rendered = append(rendered, e.FromKey+"→"+e.ToKey)
	}
	return strings.Join(rendered, ", ")
}

// The nominal cycle: open, replay, open the OTHER WAY, read, close one direction, close the other.
//
// THE SUBTEST THAT CARRIES CARD 11 is "the reverse direction is a second declaration". Under the
// pair of 000007 it returned created = false — declaring FRNT↔CORE after CORE↔FRNT touched the same
// single row. Under the directed table it returns created = true and the graph holds two rows,
// because those are two different authorisations and the customer typed both.
//
// Both verbs stay idempotent and say so — `created` and `removed` tell "done" from "it already was"
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

	// The two repos are born linked IN BOTH DIRECTIONS: creating FRNT opened its two arrows with
	// CORE in the same statement (card 12, doubled by card 11). The rest of this test is about the
	// HUMAN verbs, so it starts by closing that default — without which "the first opening" below
	// would not be one.
	t.Run("both directions are already open at creation", func(t *testing.T) {
		if got := readGraph(t, st, team); got != "CORE→FRNT, FRNT→CORE" {
			t.Errorf("graph right after the creations = %q, want %q", got, "CORE→FRNT, FRNT→CORE")
		}

		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust: %v", err)
		}
		if created {
			t.Error("created = true: the edge was already opened by the creation of the repos")
		}

		for _, e := range [][2]string{{"CORE", "FRNT"}, {"FRNT", "CORE"}} {
			removed, err := st.RevokeTrust(ctx, team.ID, e[0], e[1])
			if err != nil {
				t.Fatalf("RevokeTrust %s→%s (closing the default): %v", e[0], e[1], err)
			}
			if !removed {
				t.Fatalf("removed = false on %s→%s: there was no default edge to close", e[0], e[1])
			}
		}
		if got := readGraph(t, st, team); got != "" {
			t.Fatalf("graph after closing both defaults = %q, want empty", got)
		}
	})

	t.Run("opening", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust: %v", err)
		}
		if !created {
			t.Error("created = false on the first opening")
		}
		if got := readGraph(t, st, team); got != "CORE→FRNT" {
			t.Errorf("graph = %q, want %q: one command opens ONE direction", got, "CORE→FRNT")
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
		if got := readGraph(t, st, team); got != "CORE→FRNT" {
			t.Errorf("graph = %q after a replay, want %q: the replay wrote a row", got, "CORE→FRNT")
		}
	})

	// THE CASE CARD 11 EXISTS FOR. Naming the two projects the other way round declares a SECOND
	// edge: `FRNT → CORE` is the authorisation for FRNT to open a question at CORE, and nothing in
	// `CORE → FRNT` granted it.
	t.Run("the reverse direction is a second declaration", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("AllowTrust (reverse direction): %v", err)
		}
		if !created {
			t.Error("created = false the other way round: the two directions are still one row")
		}
		if got := readGraph(t, st, team); got != "CORE→FRNT, FRNT→CORE" {
			t.Errorf("graph = %q, want %q", got, "CORE→FRNT, FRNT→CORE")
		}
	})

	t.Run("read", func(t *testing.T) {
		edges, err := st.ListTrustEdges(ctx, team.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		if len(edges) != 2 {
			t.Fatalf("%d edges, want 2 (one per direction): %+v", len(edges), edges)
		}
		if edges[0].FromKey != "CORE" || edges[0].ToKey != "FRNT" {
			t.Errorf("first edge = %s→%s, want CORE→FRNT (ORDER BY a.key, b.key)",
				edges[0].FromKey, edges[0].ToKey)
		}
		if edges[0].CreatedAt.IsZero() {
			t.Error("created_at is zero: the declaration date is lost")
		}
	})

	// AND THIS IS THE OTHER HALF OF THE SAME GUARANTEE: cutting one direction leaves the other
	// standing. A `deny` that emptied the couple would make the model directed on paper only.
	t.Run("closing one direction leaves the other", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("RevokeTrust: %v", err)
		}
		if !removed {
			t.Error("removed = false although the edge was declared")
		}
		if got := readGraph(t, st, team); got != "CORE→FRNT" {
			t.Errorf("graph = %q, want %q: cutting FRNT→CORE took CORE→FRNT with it", got, "CORE→FRNT")
		}
	})

	t.Run("replaying the closing", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("RevokeTrust (replay): %v", err)
		}
		if removed {
			t.Error("removed = true on the replay: the command is not idempotent")
		}
	})

	t.Run("the graph is empty once the last direction is cut", func(t *testing.T) {
		if _, err := st.RevokeTrust(ctx, team.ID, "CORE", "FRNT"); err != nil {
			t.Fatalf("RevokeTrust: %v", err)
		}
		if got := readGraph(t, st, team); got != "" {
			t.Errorf("graph = %q after closing everything, want empty", got)
		}
	})
}

// A key that does not resolve yields ErrNotFound on BOTH verbs.
//
// This is the accepted departure from docs/DESIGN-TRUST.md, which planned an `:execrows` for
// RevokeTrust: a bare DELETE would have returned "nothing to remove" to a human who just typed a
// key wrong, that is, an apparent success. These routes are ADMIN and an admin already enumerates
// every project of every team: there is no oracle to protect, hence nothing to gain from keeping the
// error quiet.
//
// MUTATION: going back to `DELETE ... USING projects a, projects b` without the `edge` CTE makes
// the "closing, unknown key" subtest fail.
func TestTrustRefusesUnknownKeys(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}

	cases := []struct {
		name string
		from string
		to   string
	}{
		{"unknown recipient", "CORE", "NOPE"},
		{"unknown sender", "NOPE", "CORE"},
		{"both unknown", "NOPE", "NADA"},
		// Case is NOT normalised by the query: it is the service that uppercases. This case pins the
		// boundary — were normalisation to migrate into the SQL, it would turn green and one would
		// have to decide where it lives, rather than having it in both places.
		{"lowercase key, not normalised by the query", "core", "CORE"},
	}

	for _, c := range cases {
		t.Run("opening, "+c.name, func(t *testing.T) {
			if _, err := st.AllowTrust(ctx, team.ID, c.from, c.to); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("AllowTrust(%s, %s): error = %v, want ErrNotFound", c.from, c.to, err)
			}
		})
		t.Run("closing, "+c.name, func(t *testing.T) {
			if _, err := st.RevokeTrust(ctx, team.ID, c.from, c.to); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("RevokeTrust(%s, %s): error = %v, want ErrNotFound", c.from, c.to, err)
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

	// From my team, the neighbour's OPS key does not exist — in either direction.
	if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AllowTrust towards one of the neighbour's projects: error = %v, want ErrNotFound", err)
	}
	if _, err := st.AllowTrust(ctx, mine.ID, "OPS", "CORE"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AllowTrust from one of the neighbour's projects: error = %v, want ErrNotFound", err)
	}

	// Each team holds exactly the two arrows its two repos were born with, and sees nothing of the
	// other's.
	if got := readGraph(t, st, mine); got != "CORE→FRNT, FRNT→CORE" {
		t.Errorf("my graph = %q, want %q", got, "CORE→FRNT, FRNT→CORE")
	}
	if got := readGraph(t, st, other); got != "CORE→OPS, OPS→CORE" {
		t.Errorf("the neighbour's graph = %q, want %q", got, "CORE→OPS, OPS→CORE")
	}

	// And I cannot close theirs, even by naming their keys exactly.
	if _, err := st.RevokeTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RevokeTrust on the neighbour's edge: error = %v, want ErrNotFound", err)
	}
	if got := readGraph(t, st, other); got != "CORE→OPS, OPS→CORE" {
		t.Errorf("the neighbour's graph after my attempt = %q, want %q", got, "CORE→OPS, OPS→CORE")
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

	// MUTATION: removing `a.team_id = @team_id` from the `edge` CTE of RevokeTrust.
	//
	// The existing boundary test did not catch it: it passed OPS in SECOND position, and
	// `b.team_id` — still in place — was enough to make the resolution fail. What is needed is a key
	// that exists ONLY at the neighbour's in FIRST position, and a key of my team in second: only
	// the constraint on `a` decides then.
	//
	// Under the mutation, `a` resolves to the neighbour's OPS, `b` to my CORE, the edge resolves,
	// and the query returns `removed=false` with no error — instead of the ErrNotFound a key outside
	// my team must produce.
	t.Run("RevokeTrust scopes both ends, not just the recipient", func(t *testing.T) {
		if _, err := st.RevokeTrust(ctx, mine.ID, "OPS", "CORE"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound: OPS does not exist in my team", err)
		}
		// And the neighbour's graph is untouched.
		if got := readGraph(t, st, other); got != "CORE→OPS, OPS→CORE" {
			t.Errorf("the neighbour's graph = %q, want %q", got, "CORE→OPS, OPS→CORE")
		}
	})

	// MUTATION: replacing `a.id <> b.id` with `true` in AllowTrust.
	//
	// The self-edge refusal was only proven by the service, which validates `from != to` BEFORE
	// calling the store. The query has to refuse it too: that is the second turn of the key, and it
	// is what holds if a caller reaches the store directly.
	t.Run("AllowTrust refuses a self-edge in the query", func(t *testing.T) {
		if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "CORE"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound: the query must refuse the self-edge without "+
				"letting project_trust_not_self raise a second error path", err)
		}
		// My team holds exactly the two arrows its two repos were born with (card 12, doubled by
		// card 11). Naming them says what the graph IS, so a self-edge slipping in shows up as a
		// third row instead of hiding in a count that was already non-zero.
		if got := readGraph(t, st, mine); got != "CORE→FRNT, FRNT→CORE" {
			t.Errorf("graph = %q, want %q untouched: the self-edge wrote something", got,
				"CORE→FRNT, FRNT→CORE")
		}
	})
}
