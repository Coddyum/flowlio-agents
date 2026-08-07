package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                     | Résumé                                              | Ligne |
// |-----------------------------|------------------------------------------------------|-------|
// | TestAdminTokenPerMode       | ADMIN_TOKEN is required in hosted and refused in local | 27    |
// | TestLoadCarriesTheAdminToken| Load hands the token through untouched                 | 101   |
//
// Fin du sommaire.
// =====================================================================
//
// ADMIN_TOKEN is the one setting whose ABSENCE and whose PRESENCE are each fatal, in opposite
// modes, and neither half is obvious from reading Load. Hosted without it starts an instance that
// answers 401 to its own operator with no way in; local with it leaves a live administration secret
// in an environment that ignores it. Both are silent failures at the moment they are made.

import (
	"strings"
	"testing"
)

// dsn is any well-formed value: Load does not dial, it only reads.
const dsn = "postgres://u:p@localhost:5432/db?sslmode=disable"

// TestAdminTokenPerMode drives both halves of the mismatch and both correct combinations.
func TestAdminTokenPerMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		token   string
		wantErr string
	}{
		{
			name:    "hosted without a token is refused",
			mode:    ModeHosted,
			wantErr: "requires ADMIN_TOKEN",
		},
		{
			name:    "hosted with only whitespace is refused too",
			mode:    ModeHosted,
			token:   "   ",
			wantErr: "requires ADMIN_TOKEN",
		},
		{
			name:  "hosted with a token is accepted",
			mode:  ModeHosted,
			token: "flw_abcdefghijkl_secret",
		},
		{
			name:    "local with a token is refused rather than ignored",
			mode:    ModeLocal,
			token:   "flw_abcdefghijkl_secret",
			wantErr: "would be ignored",
		},
		{
			name: "local without a token is the ordinary case",
			mode: ModeLocal,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", dsn)
			t.Setenv("MODE", c.mode)
			t.Setenv("ADMIN_TOKEN", c.token)

			cfg, err := Load()
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("Load = %v, want nil", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("Load = nil, want an error naming %q", c.wantErr)
			case c.wantErr != "":
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("Load = %q, want it to name %q", err, c.wantErr)
				}
				if cfg != nil {
					t.Error("a rejected configuration was returned anyway")
				}
				return
			}

			// The mode's own expectation about the field, so that a Load which accepted the
			// combination but dropped the value still fails here.
			if c.mode == ModeHosted && cfg.AdminToken == "" {
				t.Error("hosted config carries no admin token")
			}
			if c.mode == ModeLocal && cfg.AdminToken != "" {
				t.Errorf("local config carries an admin token: %q", cfg.AdminToken)
			}
		})
	}
}

// TestLoadCarriesTheAdminTokenVerbatim pins the value through, surrounding whitespace excepted.
//
// A token that arrives trimmed of its prefix, lower-cased, or otherwise "cleaned" would hash to
// something the operator never holds, and the instance would refuse the very credential it was
// configured with — at start-up, on every deploy, with no clue as to why.
func TestLoadCarriesTheAdminTokenVerbatim(t *testing.T) {
	const token = "flw_abcdefghijkl_Zm9vYmFyX0JBWg"

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("MODE", ModeHosted)
	t.Setenv("ADMIN_TOKEN", "  "+token+"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AdminToken != token {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, token)
	}
}
