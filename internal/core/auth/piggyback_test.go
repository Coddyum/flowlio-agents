package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// serveThrough runs one authenticated request through Middleware wrapping a 200 handler, and returns
// the recorder so a test can read the response headers.
func serveThrough(t *testing.T, svc Service, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	h := svc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The piggyback stamps every authenticated project response with the wake state, read from the
// injected closure — no extra request (D55, DESIGN-WAKE §3). "1" when there is work, "0" when there
// is not, and absent when the closure could not answer cheaply.
func TestMiddlewarePiggybacksTheWakeState(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	project := TokenRecord{
		ID: uuid.New(), Scope: ScopeProject, TeamID: uuid.New(), ProjectID: uuid.New(),
		SecretHash: token.Hash,
	}

	cases := []struct {
		name      string
		wakeState func(Principal) (bool, bool)
		wantSet   bool
		wantValue string
	}{
		{"has work", func(Principal) (bool, bool) { return true, true }, true, "1"},
		{"no work", func(Principal) (bool, bool) { return false, true }, true, "0"},
		{"cold cache omits the header", func(Principal) (bool, bool) { return false, false }, false, ""},
		{"nil closure disables it", nil, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(&fakeStore{found: true, record: project}, tc.wakeState)
			rec := serveThrough(t, svc, token.Plain)

			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200 — the valid token did not authenticate", rec.Code)
			}
			got, set := rec.Header()[http.CanonicalHeaderKey(WakeHeader)]
			if set != tc.wantSet {
				t.Fatalf("header present = %v, want %v", set, tc.wantSet)
			}
			if tc.wantSet && got[0] != tc.wantValue {
				t.Errorf("%s = %q, want %q", WakeHeader, got[0], tc.wantValue)
			}
		})
	}
}

// An admin token carries no probe cursor: the piggyback must not stamp its responses even when the
// closure is present.
func TestMiddlewareDoesNotPiggybackAdminResponses(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	admin := TokenRecord{ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash}

	svc := New(&fakeStore{found: true, record: admin}, func(Principal) (bool, bool) { return true, true })
	rec := serveThrough(t, svc, token.Plain)

	if _, set := rec.Header()[http.CanonicalHeaderKey(WakeHeader)]; set {
		t.Error("an admin response carried the wake header — the piggyback is project-scoped")
	}
}
