package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                     | Résumé                                              | Ligne |
// |-----------------------------|------------------------------------------------------|-------|
// | TestAdminTokenPerMode       | ADMIN_TOKEN is required in hosted and refused in local | 29    |
// | TestLoadCarriesTheAdminTokenVerbatim | Load hands the token through untouched      | 103   |
// | TestAllowedOriginsSetButEmptyClosesTheSurface | Set and empty is not unset         | 127   |
//
// Fin du sommaire.
// =====================================================================
//
// ADMIN_TOKEN is the one setting whose ABSENCE and whose PRESENCE are each fatal, in opposite
// modes, and neither half is obvious from reading Load. Hosted without it starts an instance that
// answers 401 to its own operator with no way in; local with it leaves a live administration secret
// in an environment that ignores it. Both are silent failures at the moment they are made.

import (
	"os"
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

// TestAllowedOriginsSetButEmptyClosesTheSurface pins the one distinction `optional` cannot make.
//
// Writing `ALLOWED_ORIGINS=` is how an operator says "no browser origin at all". Until 2026-08-07
// that produced the two DEFAULT origins instead — the exact opposite, silently, on the setting whose
// whole job is to bound who may call the API from a browser. The function's own comment had claimed
// the correct behaviour from the first day, which is why nobody looked.
//
// Unset must still fall back, or every deployment would have to spell the default out.
func TestAllowedOriginsSetButEmptyClosesTheSurface(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  int
	}{
		{name: "unset falls back to the default", want: 2},
		{name: "set and empty closes the surface", set: true},
		{name: "set to whitespace closes it too", set: true, value: "   "},
		{name: "set to one origin yields one", set: true, value: "https://example.test", want: 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", dsn)
			t.Setenv("MODE", ModeLocal)
			if c.set {
				t.Setenv("ALLOWED_ORIGINS", c.value)
			} else if err := os.Unsetenv("ALLOWED_ORIGINS"); err != nil {
				// Not ceremony: an unset that silently failed would leave a value from an earlier
				// subtest in place, and "unset falls back to the default" would pass by accident.
				t.Fatalf("unsetting ALLOWED_ORIGINS: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.AllowedOrigins) != c.want {
				t.Fatalf("AllowedOrigins = %q (%d), want %d entries",
					cfg.AllowedOrigins, len(cfg.AllowedOrigins), c.want)
			}
			// The default is never `*`, in any mode: this API answers an administration token.
			for _, origin := range cfg.AllowedOrigins {
				if origin == "*" {
					t.Error("a wildcard origin was produced")
				}
			}
		})
	}
}
