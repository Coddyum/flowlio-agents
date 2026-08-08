package handler

// What this file locks down: the STATUS and the BODY of `DELETE /projects/{id}`.
//
// It is deliberately separate from the count assertions of the store's integration test. That one
// proves the sibling keeps its threads; this one proves the customer is told why, in words they can
// act on. Reading a status code proves nothing about the first, and counting rows proves nothing
// about the second — which is why neither test is allowed to stand in for the other.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// deleteProject plays `DELETE /projects/{id}` under a GLOBAL admin token, through the real
// middleware and the real routes, and returns the status, the body, and the fake it drove.
func deleteProject(t *testing.T, projectID uuid.UUID, err error) (int, string, *fakeWorkspace) {
	t.Helper()

	teams, _, _ := fixtures()
	svc := &fakeWorkspace{teams: teams, deleteErr: err}
	mux, raw := adminServer(t, uuid.Nil, svc)

	req := httptest.NewRequest(http.MethodDelete, "/projects/"+projectID.String()+"?team=my-team", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String(), svc
}

// A repo a sibling still talks to is refused with 409, and the body NAMES the siblings.
//
// The body is asserted literally, and that is the point of the test: `writeError` renders
// service.ErrConflict as `{"error":"already exists"}`, so a refusal routed through that sentinel
// would still answer 409 and this test would still be the only thing that noticed.
//
// MUTATION: replace the `errors.As(err, &inUse)` case of writeError with
// `errors.Is(err, service.ErrConflict)` → the code stays 409 and this test goes red on the body.
func TestDeletingARepoASiblingTalksToIsRefusedWithItsReason(t *testing.T) {
	refusal := &service.ProjectInUseError{
		Holders: []service.ThreadHolder{{Key: "CORE", Threads: 2}, {Key: "WEB", Threads: 1}},
	}

	code, body, svc := deleteProject(t, uuid.New(), refusal)

	if code != http.StatusConflict {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusConflict)
	}

	const want = `{"error":"this repo still holds questions with CORE (2 threads), WEB (1 thread),` +
		` and deleting it would erase those threads from their side too. Retire it instead: revoke` +
		` its tokens, then deny its trust edges."}`
	if body != want {
		t.Errorf("body =\n  %s\nwant\n  %s", body, want)
	}
	if !contains(svc.calls, "DeleteProject") {
		t.Errorf("the service was never asked (calls: %v)", svc.calls)
	}
}

// THE POSITIVE CONTROL for the test above: a route answering 409 to everything would satisfy it.
//
// The identifier the service receives is the one in the PATH, and the answer is 204 with no body.
//
// Both halves matter: a handler that deleted a project of its own choosing would still answer 204,
// and a 200 with a body would describe a repo that no longer exists.
func TestASuccessfulDeletionAnswers204AndTargetsThePath(t *testing.T) {
	target := uuid.New()

	code, body, svc := deleteProject(t, target, nil)

	if code != http.StatusNoContent {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusNoContent)
	}
	if body != "" {
		t.Errorf("body = %q, want it empty", body)
	}
	if svc.gotDeleted != target {
		t.Errorf("the service was asked to delete %s, the path named %s", svc.gotDeleted, target)
	}
}

// An identifier that is not a UUID is a 400 that says so, and the service is never reached: a
// malformed path must not become a lookup.
func TestAMalformedIdentifierNeverReachesTheService(t *testing.T) {
	teams, _, _ := fixtures()
	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, uuid.Nil, svc)

	req := httptest.NewRequest(http.MethodDelete, "/projects/not-a-uuid?team=my-team", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d (body %s), want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}
	if contains(svc.calls, "DeleteProject") {
		t.Errorf("the service received DeleteProject on a malformed identifier (calls: %v)", svc.calls)
	}
}
