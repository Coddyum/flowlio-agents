package waker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/waker"
)

// Claude with a known session id resumes into it; with none, and any other agent, it launches fresh.
// This is the whole of DESIGN-WAKE §4.2.
func TestLaunchArgv(t *testing.T) {
	claude, _ := waker.Preset("claude")
	codex, _ := waker.Preset("codex")

	resume := claude.LaunchArgv("sess-123", "GO")
	wantResume := []string{"claude", "-r", "sess-123", "-p", "GO", "--allowedTools", "mcp__flowlio-agents"}
	if strings.Join(resume, " ") != strings.Join(wantResume, " ") {
		t.Errorf("claude resume argv = %v, want %v", resume, wantResume)
	}

	fresh := claude.LaunchArgv("", "GO")
	wantFresh := []string{"claude", "-p", "GO", "--allowedTools", "mcp__flowlio-agents"}
	if strings.Join(fresh, " ") != strings.Join(wantFresh, " ") {
		t.Errorf("claude fresh argv = %v, want %v", fresh, wantFresh)
	}

	// codex has no resume: even with a session id it launches fresh.
	codexArgv := codex.LaunchArgv("sess-123", "GO")
	wantCodex := []string{"codex", "exec", "GO"}
	if strings.Join(codexArgv, " ") != strings.Join(wantCodex, " ") {
		t.Errorf("codex argv = %v, want %v", codexArgv, wantCodex)
	}
}

func TestPresetsAndCustom(t *testing.T) {
	for _, name := range []string{"claude", "codex", "opencode", "CLAUDE", " Codex "} {
		if _, ok := waker.Preset(name); !ok {
			t.Errorf("Preset(%q) not found", name)
		}
	}
	if _, ok := waker.Preset("mytool"); ok {
		t.Error("Preset(mytool) should be unknown")
	}

	custom, ok := waker.Custom("mytool --headless {prompt}")
	if !ok {
		t.Fatal("Custom rejected a valid template")
	}
	got := custom.LaunchArgv("", "GO")
	want := []string{"mytool", "--headless", "GO"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("custom argv = %v, want %v", got, want)
	}
	if _, ok := waker.Custom("   "); ok {
		t.Error("Custom accepted an empty template")
	}
}

// Inline substitution: {prompt} inside a larger flag is filled, not just a whole field.
func TestInlineSubstitution(t *testing.T) {
	custom, _ := waker.Custom("mytool --message={prompt}")
	got := custom.LaunchArgv("", "hello world")
	if got[1] != "--message=hello world" {
		t.Errorf("inline argv[1] = %q, want %q", got[1], "--message=hello world")
	}
}

// The cap is a sliding window: n launches allowed, the (n+1)th within the window refused, and the
// window sliding on lets launches through again. This is the loop-breaker of §9.
func TestCapSlidingWindow(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	cap := waker.NewCap(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !cap.Allow("CORE", base) {
			t.Fatalf("launch %d refused within the limit", i+1)
		}
	}
	if cap.Allow("CORE", base) {
		t.Fatal("the fourth launch in the window was allowed — the cap did not hold")
	}

	// A different repo has its own budget.
	if !cap.Allow("WEB", base) {
		t.Error("a second repo was refused on the first repo's budget")
	}

	// The window slides on: launches are allowed again.
	if !cap.Allow("CORE", base.Add(61*time.Second)) {
		t.Error("CORE was still capped after the window elapsed")
	}
}

func TestBearerOK(t *testing.T) {
	secret := "handshake-secret"
	if !waker.BearerOK("Bearer "+secret, secret) {
		t.Error("the matching bearer was rejected")
	}
	if waker.BearerOK("Bearer wrong", secret) {
		t.Error("a wrong bearer was accepted")
	}
	if waker.BearerOK(secret, secret) {
		t.Error("a header without the Bearer prefix was accepted")
	}
	if waker.BearerOK("", secret) {
		t.Error("an empty header was accepted")
	}
}

// A wake with the secret is accepted and launches; without it, refused and silent. This is the §9
// guard on the loopback endpoint.
func TestListenerVerifiesTheSecret(t *testing.T) {
	woke := make(chan struct{}, 1)
	l := waker.NewListener("s3cr3t", func() { woke <- struct{}{} })
	srv := httptest.NewServer(l)
	defer srv.Close()

	post := func(auth string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"project":"x"}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("Bearer s3cr3t"); code != http.StatusAccepted {
		t.Fatalf("valid wake code = %d, want 202", code)
	}
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("a valid wake did not launch")
	}

	if code := post("Bearer wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong-secret wake code = %d, want 401", code)
	}
	if code := post(""); code != http.StatusUnauthorized {
		t.Errorf("no-secret wake code = %d, want 401", code)
	}
	select {
	case <-woke:
		t.Fatal("a refused wake still launched")
	case <-time.After(100 * time.Millisecond):
	}
}

// Launch runs under the cap: the first goes through, and once the cap is full the wake is dropped
// with no launch — the loop-breaker of §9, end to end.
func TestLaunchHonoursTheCap(t *testing.T) {
	base := time.Unix(3_000_000, 0)
	cap := waker.NewCap(1, time.Minute)
	var runs int
	run := func(context.Context, string, []string) error { runs++; return nil }
	repo := waker.Repo{Key: "CORE", Path: "/tmp/core"}
	repo.Agent, _ = waker.Preset("codex")

	launched, err := waker.Launch(context.Background(), cap, run, repo, base)
	if err != nil || !launched {
		t.Fatalf("first launch: launched=%v err=%v", launched, err)
	}
	launched, _ = waker.Launch(context.Background(), cap, run, repo, base)
	if launched {
		t.Fatal("second launch went through despite a cap of 1")
	}
	if runs != 1 {
		t.Errorf("the launcher ran %d times, want 1", runs)
	}
}

func TestProbeDelayHasAFloor(t *testing.T) {
	if got := waker.ProbeDelay(0); got != 30*time.Second {
		t.Errorf("ProbeDelay(0) = %s, want 30s floor", got)
	}
	if got := waker.ProbeDelay(120); got != 2*time.Minute {
		t.Errorf("ProbeDelay(120) = %s, want 2m", got)
	}
}

func TestNewSecretIsRandomHex(t *testing.T) {
	a, err := waker.NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	b, _ := waker.NewSecret()
	if a == b {
		t.Error("two secrets came out identical")
	}
	if len(a) != 64 { // 32 bytes hex-encoded
		t.Errorf("secret length = %d, want 64", len(a))
	}
}
