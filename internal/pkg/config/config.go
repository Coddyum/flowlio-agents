package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                            | Ligne |
// |----------|-------------------------------------------------------------------|-------|
// | Config   | Configuration of the process, read once at start-up                 | 41    |
// | Config.IsLocal | Says whether the process runs in local mode, without accounts | 60    |
// | Load     | Reads the environment and fails at once if a key is missing         | 66    |
// | required | Yields an environment variable, or an error if missing or empty     | 87    |
// | list     | Splits an environment variable into a list, on the commas           | 98    |
// | optional | Yields an environment variable or the default value                 | 111   |
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

	return &Config{
		Addr:           optional("ADDR", defaultAddr),
		DatabaseURL:    dbURL,
		Env:            optional("ENV", defaultEnv),
		Mode:           mode,
		AllowedOrigins: list("ALLOWED_ORIGINS", defaultAllowedOrigins),
	}, nil
}

// required yields the environment variable key, or an error if it is missing or empty.
func required(key string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return "", fmt.Errorf("missing environment variable %s", key)
	}
	return v, nil
}

// list splits an environment variable into a list, on the commas, throwing away the empty
// entries. An explicitly empty value yields an empty list: that is how a surface is closed
// completely, and it has to be expressible.
func list(key, def string) []string {
	raw := optional(key, def)

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
