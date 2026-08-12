package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// probeOnce hits flowlio-core's relay with the account bearer, launches on work, and honours the
// cadence the server dictates — including a 429's Retry-After. This is the hosted transport of
// DESIGN-WAKE §6, without a live core.
func TestHostedProbeOnce(t *testing.T) {
	t.Run("work launches at the suggested effort and honours next_probe_after", func(t *testing.T) {
		var gotAuth, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.RequestURI()
			_, _ = w.Write([]byte(`{"has_work":true,"next_probe_after":120,"suggested_effort":"high"}`))
		}))
		defer srv.Close()

		launched := 0
		gotEffort := ""
		delay := probeOnce(context.Background(), client.New(srv.URL, "acct-pat"), "CORE",
			"/api/v2/agents/wake?repo=abc", func(effort string) { launched++; gotEffort = effort })

		if gotAuth != "Bearer acct-pat" {
			t.Errorf("Authorization = %q, want the account bearer", gotAuth)
		}
		if gotPath != "/api/v2/agents/wake?repo=abc" {
			t.Errorf("relay path = %q, want the wake relay with the repo id", gotPath)
		}
		if launched != 1 {
			t.Errorf("launched %d times, want 1 (has_work was true)", launched)
		}
		if gotEffort != "high" {
			t.Errorf("launched at effort %q, want %q from suggested_effort", gotEffort, "high")
		}
		if delay != 2*time.Minute {
			t.Errorf("delay = %s, want 2m (next_probe_after=120)", delay)
		}
	})

	t.Run("no work does not launch", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"has_work":false,"next_probe_after":60}`))
		}))
		defer srv.Close()

		launched := 0
		delay := probeOnce(context.Background(), client.New(srv.URL, "x"), "CORE", "/api/v2/agents/wake?repo=abc", func(string) { launched++ })
		if launched != 0 {
			t.Errorf("launched %d times on an empty probe, want 0", launched)
		}
		if delay < 30*time.Second {
			t.Errorf("delay = %s, below the floor", delay)
		}
	})

	t.Run("429 backs off by Retry-After and does not launch", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "300")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"has_work":false,"next_probe_after":300}`))
		}))
		defer srv.Close()

		launched := 0
		delay := probeOnce(context.Background(), client.New(srv.URL, "x"), "CORE", "/api/v2/agents/wake?repo=abc", func(string) { launched++ })
		if launched != 0 {
			t.Errorf("a throttled probe launched %d times, want 0", launched)
		}
		if delay != 5*time.Minute {
			t.Errorf("delay = %s, want 5m (Retry-After=300)", delay)
		}
	})
}
