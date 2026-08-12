package main

import (
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// The session id filed by the SessionStart hook round-trips, and the waker reads it back to resume.
// An unknown repo reads "" — which the waker takes as "launch fresh".
func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	rf := credentials.RepoFile{Project: "flowlio", Repo: "CORE"}
	if got := loadSession(rf); got != "" {
		t.Errorf("loadSession before any save = %q, want empty", got)
	}
	if err := saveSession(rf, "sess-abc-123"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if got := loadSession(rf); got != "sess-abc-123" {
		t.Errorf("loadSession = %q, want sess-abc-123", got)
	}
	// The newest wins: a second session overwrites the first.
	if err := saveSession(rf, "sess-def-456"); err != nil {
		t.Fatalf("saveSession again: %v", err)
	}
	if got := loadSession(rf); got != "sess-def-456" {
		t.Errorf("loadSession after overwrite = %q, want sess-def-456", got)
	}
}

// Two hosted repositories that share a key (CORE) but not an id keep separate sessions: the session
// file follows the credential, which is keyed by id in hosted. This is the regression guarding the
// 2026-08-12 collision, where a second `flowlio connect --id` buried the first repo's state.
func TestHostedSessionsDoNotCollideOnKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	omiros := credentials.RepoFile{Project: "hosted", Repo: "CORE", RepoID: "id-omiros"}
	flowlio := credentials.RepoFile{Project: "hosted", Repo: "CORE", RepoID: "id-flowlio"}

	if err := saveSession(omiros, "sess-omiros"); err != nil {
		t.Fatalf("saveSession omiros: %v", err)
	}
	if err := saveSession(flowlio, "sess-flowlio"); err != nil {
		t.Fatalf("saveSession flowlio: %v", err)
	}

	if got := loadSession(omiros); got != "sess-omiros" {
		t.Errorf("omiros session = %q, want sess-omiros — flowlio overwrote it", got)
	}
	if got := loadSession(flowlio); got != "sess-flowlio" {
		t.Errorf("flowlio session = %q, want sess-flowlio", got)
	}
}

// repoForDir maps a working directory back to the connected repo whose path it is — how the
// SessionStart hook names a repo with no argument.
func TestRepoForDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := credentials.SaveRepo(credentials.RepoFile{
		APIURL: "http://127.0.0.1:1", Project: "flowlio", Repo: "CORE", Token: "flw_x", Path: dir,
	}); err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}

	rf, err := repoForDir(dir)
	if err != nil {
		t.Fatalf("repoForDir(%q): %v", dir, err)
	}
	if rf.Repo != "CORE" {
		t.Errorf("repoForDir resolved %q, want CORE", rf.Repo)
	}

	if _, err := repoForDir(t.TempDir()); err == nil {
		t.Error("repoForDir on an unconnected directory returned no error")
	}
}
