package store_test

// FLWL-70, PART 5 — the note quota, against Postgres.
//
// WHY THIS FILE CANNOT BE A UNIT TEST. The whole guarantee lives in one SQL statement: read,
// compare and write in a single UPDATE, so that two concurrent notes cannot both read the same
// total and both get through. A double reproducing that would prove the double.
//
// THE QUOTA IS NOT REACHED BY WRITING 64 MiB. The counter is pre-loaded to just under the bound
// through direct SQL, which is exactly the state a project reaches after a long life. What is
// under test is the PREDICATE, and the predicate cannot tell how the counter got there.

import (
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// noteBytes reads the project's counter.
func noteBytes(t *testing.T, st store.Store, sc scope) int64 {
	t.Helper()
	_, db := newStore(t)
	var got int64
	if err := db.QueryRow("SELECT note_bytes FROM projects WHERE id = $1", sc.projectID).Scan(&got); err != nil {
		t.Fatalf("reading note_bytes: %v", err)
	}
	return got
}

// preload sets the counter, so a test can stand at the bound without writing 64 MiB.
func preload(t *testing.T, sc scope, value int64) {
	t.Helper()
	_, db := newStore(t)
	if _, err := db.Exec("UPDATE projects SET note_bytes = $1 WHERE id = $2", value, sc.projectID); err != nil {
		t.Fatalf("preloading note_bytes: %v", err)
	}
}

// A charge accumulates on the project row. Without this case, a quota that refuses everything
// would pass the refusal test below.
func TestChargingNoteBytesAccumulates(t *testing.T) {
	st, db := newStore(t)
	sc := newProject(t, db, "QUOTA")

	for _, n := range []int64{100, 250, 3} {
		if err := st.ChargeNoteBytes(t.Context(), sc.teamID, sc.projectID, n); err != nil {
			t.Fatalf("charging %d: %v", n, err)
		}
	}

	if got := noteBytes(t, st, sc); got != 353 {
		t.Errorf("note_bytes = %d, want 353", got)
	}
}

// THE BOUND ITSELF. One byte below it the charge passes; the charge that would cross it is
// refused, and — this is the half a status code alone would miss — the counter DOES NOT MOVE.
//
// A refusal that had already debited would make the quota permanent: the project would stay above
// the bound for ever, refusing even the notes it still had room for.
//
// MUTATION: remove `AND note_bytes + @bytes <= @quota` from ChargeProjectNoteBytes — this test
// goes red on the refusal and on the counter.
func TestTheChargeCrossingTheBoundIsRefusedAndDebitsNothing(t *testing.T) {
	st, db := newStore(t)
	sc := newProject(t, db, "QUOTB")

	preload(t, sc, store.ProjectNoteBytesQuota-10)

	// Exactly at the bound: allowed. `<=`, not `<`.
	if err := st.ChargeNoteBytes(t.Context(), sc.teamID, sc.projectID, 10); err != nil {
		t.Fatalf("a charge landing exactly on the bound was refused: %v", err)
	}
	if got := noteBytes(t, st, sc); got != store.ProjectNoteBytesQuota {
		t.Fatalf("note_bytes = %d, want %d", got, store.ProjectNoteBytesQuota)
	}

	// One byte past it: refused.
	err := st.ChargeNoteBytes(t.Context(), sc.teamID, sc.projectID, 1)
	if !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
	if got := noteBytes(t, st, sc); got != store.ProjectNoteBytesQuota {
		t.Errorf("note_bytes = %d after the refusal, want %d — a refused charge was debited, and "+
			"the project is now permanently over its bound", got, store.ProjectNoteBytesQuota)
	}
}

// The charge carries its tenancy scope: a project named under the WRONG team is not charged, and
// its counter does not move.
//
// This is the same clause every other write of the repository carries, and the same reason: the
// pair (team, project) comes from the token, and a query trusting only half of it would let a
// caller spend a neighbour's quota — a cheap way to lock another repo's journal.
//
// MUTATION: remove `AND team_id = @team_id` — this test goes red.
func TestChargingUnderTheWrongTeamIsRefused(t *testing.T) {
	st, db := newStore(t)
	sc := newProject(t, db, "QUOTC")
	other := newProject(t, db, "QUOTD")

	err := st.ChargeNoteBytes(t.Context(), other.teamID, sc.projectID, 500)
	if !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if got := noteBytes(t, st, sc); got != 0 {
		t.Errorf("note_bytes = %d, want 0 — a neighbouring team spent this project's quota", got)
	}
}
