package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// emptyStore is a bootstrap store on a database that has never issued a token: the first-run path.
type emptyStore struct{ created bool }

func (s *emptyStore) CountTokens(context.Context) (int64, error) { return 0, nil }

func (s *emptyStore) CreateAdminToken(_ context.Context, _, _, _ string) error {
	s.created = true
	return nil
}

func (s *emptyStore) RevokeAdminTokens(context.Context) (int64, error) { return 0, nil }

// TestBootstrapLocalNeverPrintsTheSecret is the guarantee that keeps a live admin credential out of
// `docker logs`, where it would be durable and readable by anything that reaches the daemon.
//
// The assertion is on the EXACT output, not on the absence of the token. "Does not contain the
// secret" passes just as well when the output is empty, or when a later line reintroduces the token
// in another shape; pinning the literal fails on any of that.
func TestBootstrapLocalNeverPrintsTheSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "flowlio", "credentials.json")

	st := &emptyStore{}
	var out strings.Builder
	if err := bootstrapLocal(context.Background(), st, ":42058", &out); err != nil {
		t.Fatalf("bootstrapLocal: %v", err)
	}
	if !st.created {
		t.Fatal("no admin token was created on a database with none")
	}

	want := fmt.Sprintf("\n  flowlio — first run\n"+
		"  Admin token created and stored, never printed.\n"+
		"  Credentials: %s (0600)\n"+
		"\n  From the repository you want to track:\n"+
		"    flowlio init --team <slug> --project <KEY>\n", path)
	if out.String() != want {
		t.Errorf("first-run output:\n%q\nwant:\n%q", out.String(), want)
	}

	// And the test is not vacuous: a real secret WAS issued, into the file and nowhere else.
	saved, err := credentials.Load()
	if err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}
	if !strings.HasPrefix(saved.Token, "flw_") {
		t.Fatalf("saved token %q does not look like one — the output check would prove nothing", saved.Token)
	}
	if saved.APIURL != "http://localhost:42058" {
		t.Errorf("saved api_url = %q, want http://localhost:42058", saved.APIURL)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", perm)
	}
}

// populatedStore stands for any later start: tokens already exist, so nothing is issued.
type populatedStore struct{ created bool }

func (s *populatedStore) CountTokens(context.Context) (int64, error) { return 3, nil }

func (s *populatedStore) CreateAdminToken(_ context.Context, _, _, _ string) error {
	s.created = true
	return nil
}

func (s *populatedStore) RevokeAdminTokens(context.Context) (int64, error) { return 3, nil }

// TestBootstrapLocalIsSilentAfterTheFirstRun: a container restarts far more often than an instance
// is created. A second start must issue nothing and say nothing, or the credentials file of a live
// instance would be overwritten by a token the database does not know.
func TestBootstrapLocalIsSilentAfterTheFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	st := &populatedStore{}
	var out strings.Builder
	if err := bootstrapLocal(context.Background(), st, ":42058", &out); err != nil {
		t.Fatalf("bootstrapLocal: %v", err)
	}
	if st.created {
		t.Error("a second admin token was issued on a database that already had some")
	}
	if out.String() != "" {
		t.Errorf("output = %q, want nothing on a restart", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "flowlio", "credentials.json")); err == nil {
		t.Error("the credentials file was rewritten on a restart — a live instance would lose its token")
	}
}
