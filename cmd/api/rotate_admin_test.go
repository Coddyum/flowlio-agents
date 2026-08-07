package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                          | Résumé                                          | Ligne |
// |----------------------------------|--------------------------------------------------|-------|
// | rotatingStore                    | A store that records the order of the two writes   | 31    |
// | rotatingStore.CountTokens        | Never called by a rotation, and says so            | 42    |
// | rotatingStore.CreateAdminToken   | Records the issued token and what preceded it      | 47    |
// | rotatingStore.RevokeAdminTokens  | Records the revocation and how many it covered     | 53    |
// | TestRotateAdminRevokesThenIssues | The rotation replaces, and never prints the secret | 67    |
// | TestRotateAdminRefusesHostedMode | Hosted mode is not this binary's to rotate         | 128   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/config"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// rotatingStore records the two writes of a rotation AND their order, which is the only thing that
// distinguishes a rotation from a second key cut for the same lock.
type rotatingStore struct {
	revoked        int64
	revokedBefore  bool
	created        bool
	countedTokens  bool
	revokeCalled   bool
	createdAfterOK bool
}

// CountTokens must never be called: a rotation replaces whatever is there, and asking how many
// tokens exist would reintroduce the very condition that locks a lost installation out.
func (s *rotatingStore) CountTokens(context.Context) (int64, error) {
	s.countedTokens = true
	return 0, nil
}

func (s *rotatingStore) CreateAdminToken(_ context.Context, _, _, _ string) error {
	s.created = true
	s.createdAfterOK = s.revokeCalled
	return nil
}

func (s *rotatingStore) RevokeAdminTokens(context.Context) (int64, error) {
	s.revokeCalled = true
	s.revokedBefore = !s.created
	s.revoked = 2
	return s.revoked, nil
}

// TestRotateAdminRevokesThenIssues is the way back in, proven end to end minus the database: the
// lost token stops being live, a new one lands in the credentials file, and the output names the
// path and nothing else.
//
// The output is pinned EXACTLY rather than checked for the absence of the secret. "Does not contain
// the token" passes just as well on an empty output, and stays green the day a line reintroduces
// the secret in another shape — the same trap the first-run test names.
func TestRotateAdminRevokesThenIssues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "flowlio", "credentials.json")

	st := &rotatingStore{}
	cfg := &config.Config{Addr: ":42058", Mode: config.ModeLocal}
	var out strings.Builder
	if err := rotateAdmin(context.Background(), st, cfg, &out); err != nil {
		t.Fatalf("rotateAdmin: %v", err)
	}

	if !st.revokeCalled || !st.created {
		t.Fatalf("revoked = %v, created = %v — a rotation does both", st.revokeCalled, st.created)
	}
	if !st.revokedBefore || !st.createdAfterOK {
		t.Error("the new token was issued BEFORE the old ones were revoked: a failure in between " +
			"would then leave two live admin tokens, one of them the lost one")
	}
	if st.countedTokens {
		t.Error("the rotation counted the existing tokens — that condition is what locks a lost " +
			"installation out, and it must not be reintroduced here")
	}

	want := fmt.Sprintf("\n  flowlio — admin token rotated\n"+
		"  2 previous token(s) revoked. New token stored, never printed.\n"+
		"  Credentials: %s (0600)\n"+
		"\n  Every project token stays valid: only the administration ones were replaced.\n", path)
	if out.String() != want {
		t.Errorf("output:\n%q\nwant:\n%q", out.String(), want)
	}

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

// TestRotateAdminRefusesHostedMode: there the token is not lost in the sense this command repairs.
// It lives in the deployment's secret store, and rotating it means minting a new one and replacing
// the environment variable. Revoking from this side would cut off the co-deployed flowlio-core
// mid-flight, silently, since the caller asked for exactly that.
//
// The assertion used to require the word "flowlio-core", back when the refusal claimed that admin
// tokens there "follow from an account and flowlio-core owns them". Nothing in either repository
// ever implemented that, so the message sent an operator somewhere no answer existed; it now names
// ADMIN_TOKEN, which does.
func TestRotateAdminRefusesHostedMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	st := &rotatingStore{}
	cfg := &config.Config{Addr: ":42058", Mode: config.ModeHosted}
	var out strings.Builder

	err := rotateAdmin(context.Background(), st, cfg, &out)
	if err == nil {
		t.Fatal("rotation accepted in hosted mode")
	}
	if !strings.Contains(err.Error(), "ADMIN_TOKEN") {
		t.Errorf("the refusal does not say where the answer lives: %v", err)
	}
	if st.revokeCalled || st.created {
		t.Error("the refusal came after writing — nothing may be written before it")
	}
	if _, err := os.Stat(filepath.Join(dir, "flowlio", "credentials.json")); err == nil {
		t.Error("the credentials file was written by a refused rotation")
	}
}
