package main

import (
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// The session id filed by the SessionStart hook round-trips, and the waker reads it back to resume.
// An unknown repo reads "" — which the waker takes as "launch fresh".
func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if got := loadSession("flowlio", "CORE"); got != "" {
		t.Errorf("loadSession before any save = %q, want empty", got)
	}
	if err := saveSession("flowlio", "CORE", "sess-abc-123"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if got := loadSession("flowlio", "CORE"); got != "sess-abc-123" {
		t.Errorf("loadSession = %q, want sess-abc-123", got)
	}
	// The newest wins: a second session overwrites the first.
	if err := saveSession("flowlio", "CORE", "sess-def-456"); err != nil {
		t.Fatalf("saveSession again: %v", err)
	}
	if got := loadSession("flowlio", "CORE"); got != "sess-def-456" {
		t.Errorf("loadSession after overwrite = %q, want sess-def-456", got)
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
