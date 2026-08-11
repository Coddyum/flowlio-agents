package wakepush_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

func newCache() cache.Cache { return cache.NewMemory(time.Hour, time.Hour) }

func TestRegisterLookupRoundtrip(t *testing.T) {
	c := newCache()
	team, proj := uuid.New(), uuid.New()
	reg := wakepush.Registration{Callback: "http://127.0.0.1:9999/wake", Secret: "s3cr3t"}

	wakepush.Register(c, team, proj, reg, time.Hour)

	got, ok := wakepush.Lookup(c, team, proj)
	if !ok {
		t.Fatal("registration not found after Register")
	}
	if got != reg {
		t.Errorf("got %+v, want %+v", got, reg)
	}

	// Scoped: another project of the same team has no registration.
	if _, ok := wakepush.Lookup(c, team, uuid.New()); ok {
		t.Error("a sibling project read this project's registration")
	}
}

// The lease is the whole pruning mechanism: an unrefreshed registration expires on its own.
func TestLeaseExpires(t *testing.T) {
	c := newCache()
	team, proj := uuid.New(), uuid.New()

	wakepush.Register(c, team, proj, wakepush.Registration{Callback: "http://localhost:1/wake", Secret: "x"}, 20*time.Millisecond)
	if _, ok := wakepush.Lookup(c, team, proj); !ok {
		t.Fatal("registration missing before the lease elapsed")
	}

	time.Sleep(40 * time.Millisecond)
	if _, ok := wakepush.Lookup(c, team, proj); ok {
		t.Error("registration still present after the lease elapsed — it did not expire")
	}
}

func TestLoopbackOnly(t *testing.T) {
	cases := []struct {
		callback string
		want     bool
	}{
		{"http://127.0.0.1:8080/wake", true},
		{"http://localhost:8080/wake", true},
		{"http://[::1]:8080/wake", true},
		{"https://127.0.0.1/wake", true},
		{"http://10.0.0.5:8080/wake", false},
		{"http://example.com/wake", false},
		{"ftp://127.0.0.1/wake", false},
		{"not a url at all", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := wakepush.LoopbackOnly(tc.callback); got != tc.want {
			t.Errorf("LoopbackOnly(%q) = %v, want %v", tc.callback, got, tc.want)
		}
	}
}

// Signal pushes to the registered waker with its secret, off the request path. This is the engine
// half of "POST /wake local" (DESIGN-WAKE §11.2).
func TestSignalPostsToRegisteredWakerWithItsSecret(t *testing.T) {
	c := newCache()
	team, proj := uuid.New(), uuid.New()

	type received struct {
		auth    string
		project string
	}
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		got <- received{auth: r.Header.Get("Authorization"), project: payload["project"]}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wakepush.Register(c, team, proj, wakepush.Registration{Callback: srv.URL, Secret: "handshake"}, time.Hour)
	wakepush.Signal(c, team, proj)

	select {
	case r := <-got:
		if r.auth != "Bearer handshake" {
			t.Errorf("Authorization = %q, want %q", r.auth, "Bearer handshake")
		}
		if r.project != proj.String() {
			t.Errorf("project in body = %q, want %q", r.project, proj.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waker was never called")
	}
}

// No registration: Signal is a silent no-op, never a panic and never a call.
func TestSignalWithoutRegistrationIsNoop(t *testing.T) {
	c := newCache()
	wakepush.Signal(c, uuid.New(), uuid.New())
	// Nothing to assert beyond "did not panic"; give any stray goroutine a moment to misbehave.
	time.Sleep(20 * time.Millisecond)
}
