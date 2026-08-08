package handler

// What this file locks down: the STATUS and the TARGET of `DELETE /teams/{slug}`.
//
// It proves nothing about what the deletion removes — that is the job of the store's integration
// test, which counts the rows. Neither test may stand in for the other: a status cannot say what is
// gone, and a row count cannot say what the customer was told.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// deleteTeam plays `DELETE /teams/{slug}` under a GLOBAL admin token, through the real middleware
// and the real routes, and returns the status, the body, and the fake it drove.
//
// The fixture is passed IN rather than built here: `fixtures()` draws fresh identifiers on every
// call, so a helper building its own would hand the test a `mine.ID` that is not the one the handler
// resolved — and the assertion on the target would compare two unrelated UUIDs.
func deleteTeam(t *testing.T, teams map[string]service.Team, slug string, err error) (int, string, *fakeWorkspace) {
	t.Helper()

	svc := &fakeWorkspace{teams: teams, deleteTeamErr: err}
	mux, raw := adminServer(t, uuid.Nil, svc)

	req := httptest.NewRequest(http.MethodDelete, "/teams/"+slug, nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec.Code, strings.TrimSpace(rec.Body.String()), svc
}

// A deletion that went through answers 204 with no body, and the service is handed the team the
// SLUG in the path resolved to — not one the handler chose.
//
// Both halves matter. A handler deleting a team of its own choosing would still answer 204, and
// this is the only assertion that would notice.
func TestDeletingATeamAnswers204AndTargetsTheSlugInThePath(t *testing.T) {
	teams, mine, _ := fixtures()

	code, body, svc := deleteTeam(t, teams, mine.Slug, nil)

	if code != http.StatusNoContent {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusNoContent)
	}
	if body != "" {
		t.Errorf("body = %q, want it empty", body)
	}
	if svc.gotDeletedTeam != mine.ID {
		t.Errorf("the service was asked to delete %s, the slug %q names %s",
			svc.gotDeletedTeam, mine.Slug, mine.ID)
	}
}

// A slug that names nothing is a 404, and the service is never reached: an unknown team must not
// become a deletion attempt.
func TestDeletingAnUnknownTeamNeverReachesTheService(t *testing.T) {
	teams, _, _ := fixtures()

	code, body, svc := deleteTeam(t, teams, "team-that-does-not-exist", nil)

	if code != http.StatusNotFound {
		t.Fatalf("code = %d, want %d", code, http.StatusNotFound)
	}
	if body != `{"error":"not found"}` {
		t.Errorf("body = %s, want {\"error\":\"not found\"}", body)
	}
	if contains(svc.calls, "DeleteTeam") {
		t.Errorf("the service received DeleteTeam for a team that does not exist (calls: %v)", svc.calls)
	}
}

// A team that vanished between the resolution and the deletion is a 404 too, not a 500. It is the
// one race this route has — two administrators pressing the same button — and it is not an outage.
//
// This is also what proves the fake can REFUSE: without deleteTeamErr, no test here would ever
// exercise the error path of the handler at all.
func TestATeamDeletedTwiceIsNotAnInternalError(t *testing.T) {
	teams, mine, _ := fixtures()

	code, body, svc := deleteTeam(t, teams, mine.Slug, service.ErrNotFound)

	if code != http.StatusNotFound {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusNotFound)
	}
	if body != `{"error":"not found"}` {
		t.Errorf("body = %s, want {\"error\":\"not found\"}", body)
	}
	if !contains(svc.calls, "DeleteTeam") {
		t.Errorf("the service was never asked (calls: %v)", svc.calls)
	}
}
