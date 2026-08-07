package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                            | Ligne |
// |----------|-------------------------------------------------------------------|-------|
// | Config   | Configuration of the process, read once at start-up                 | 42    |
// | Config.IsLocal | Says whether the process runs in local mode, without accounts | 70    |
// | Load     | Reads the environment and fails at once if a key is missing         | 76    |
// | adminTokenFor | Refuses ADMIN_TOKEN missing in hosted, and present in local    | 111   |
// | required | Yields an environment variable, or an error if missing or empty     | 126   |
// | list     | Splits an environment variable into a list, on the commas           | 144   |
// | optional | Yields an environment variable or the default value                 | 160   |
//
// Fin du sommaire.
// =====================================================================

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultAddr = ":8080"
	defaultEnv  = "dev"
	defaultMode = ModeLocal
	// defaultAllowedOrigins is the origin of the bridge page, and it alone. The user's browser
	// loads there a page served by flowlio.me that calls their local API; no other origin has any
	// reason to talk to this process.
	defaultAllowedOrigins = "https://flowlio.me,https://www.flowlio.me"
)

// Deployment modes. Local: no account, bootstrap through an admin token written to disk.
// Hosted: accounts and billing, bootstrap disabled.
const (
	ModeLocal  = "local"
	ModeHosted = "hosted"
)

// Config carries the configuration of the process. Immutable after Load: no feature writes it.
type Config struct {
	// Addr is the listen address of the HTTP server (e.g. ":8080").
	Addr string
	// DatabaseURL is the full Postgres DSN.
	DatabaseURL string
	// Env is "dev", "staging" or "prod".
	Env string
	// Mode is ModeLocal or ModeHosted and decides the bootstrap and the modules mounted.
	Mode string
	// AdminToken is the administration token of a HOSTED instance, handed over by the environment
	// and never issued by this process.
	//
	// Local mode issues its own on first start and writes it to a 0600 file. Hosted mode cannot:
	// there is no operator sitting at the machine to read that file, and the process that needs the
	// secret — the co-deployed flowlio-core — is a sibling, not a child. The secret therefore comes
	// from the deployment's secret store, which both processes already read, and this server only
	// registers its hash. Empty in local mode, where setting it is refused rather than ignored.
	AdminToken string
	// AllowedOrigins lists the web origins allowed to call the API from a browser.
	//
	// Empty by default would be safer still, but would make the bridge page unusable without
	// configuration, that is to say for everybody. The default is therefore the product's origin
	// and nothing else: `*` is never an acceptable value here, dev included — this API answers to
	// an admin token that lives on the user's machine.
	AllowedOrigins []string
}

// IsLocal says whether the process runs in local mode, without accounts.
func (c *Config) IsLocal() bool {
	return c.Mode == ModeLocal
}

// Load reads the configuration from the environment. Fail fast: a missing required key yields an
// error, the process does not start in a partial state.
func Load() (*Config, error) {
	dbURL, err := required("DATABASE_URL")
	if err != nil {
		return nil, fmt.Errorf("config: load: %w", err)
	}

	mode := optional("MODE", defaultMode)
	if mode != ModeLocal && mode != ModeHosted {
		return nil, fmt.Errorf("config: unknown MODE=%q (expected %q or %q)", mode, ModeLocal, ModeHosted)
	}

	adminToken, err := adminTokenFor(mode)
	if err != nil {
		return nil, err
	}

	return &Config{
		Addr:           optional("ADDR", defaultAddr),
		DatabaseURL:    dbURL,
		Env:            optional("ENV", defaultEnv),
		Mode:           mode,
		AdminToken:     adminToken,
		AllowedOrigins: list("ALLOWED_ORIGINS", defaultAllowedOrigins),
	}, nil
}

// adminTokenFor reads ADMIN_TOKEN and refuses both halves of the mismatch.
//
// Missing in hosted mode is fatal: the instance would start, answer 401 to its own operator, and
// there would be no way in — hosted mode issues nothing on its own, by design.
//
// PRESENT in local mode is fatal too, and that is the less obvious half. Local mode issues its own
// token and would simply ignore this one, leaving a live administration secret sitting in an
// environment where the operator believes it is in use. A credential that is configured and
// ignored is worse than one that is missing: nothing ever says so.
func adminTokenFor(mode string) (string, error) {
	token := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))

	if mode == ModeHosted && token == "" {
		return "", fmt.Errorf("config: MODE=%s requires ADMIN_TOKEN "+
			"(mint one with `flowlio-api mint-admin-token`)", ModeHosted)
	}
	if mode == ModeLocal && token != "" {
		return "", fmt.Errorf("config: ADMIN_TOKEN is set but MODE=%s, "+
			"where the first start issues its own token and this one would be ignored", ModeLocal)
	}
	return token, nil
}

// required yields the environment variable key, or an error if it is missing or empty.
func required(key string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return "", fmt.Errorf("missing environment variable %s", key)
	}
	return v, nil
}

// list splits an environment variable into a list, on the commas, throwing away the empty entries.
//
// SET AND EMPTY IS NOT UNSET, and the whole value of this function is in telling them apart. An
// operator who writes `ALLOWED_ORIGINS=` is closing the surface completely; handing them the
// default there gives them the exact opposite of what they asked for, and says nothing about it.
//
// It read through `optional` until 2026-08-07, which collapses both cases into the default — so
// this function's own comment, which has claimed since the first day that an explicitly empty value
// yields an empty list, described something the code did not do. Measured: `ALLOWED_ORIGINS=` gave
// back the two default origins. `os.LookupEnv` is what makes the distinction expressible at all.
func list(key, def string) []string {
	raw, set := os.LookupEnv(key)
	if !set {
		raw = def
	}

	out := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// optional yields the environment variable key, or def if it is missing or empty.
func optional(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
