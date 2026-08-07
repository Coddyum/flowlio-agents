package main

// THE WAY BACK IN, and the reason it exists at all.
//
// The server keeps a hash of the admin token and nothing else, and the first-run bootstrap issues
// nothing as long as the table holds a token. Lose that secret and the installation answers 401 to
// its own operator, forever, with the database as the only way back — which is a `psql` and a hand
// written `UPDATE` on a hash.
//
// On a laptop that is a bad afternoon. On an installation someone else runs it is a lockout, and
// the repository has been public since 2026-08-03, so "someone else" is no longer hypothetical.
//
// What authorises the rotation is being able to START THIS PROCESS: same binary, same DSN, same
// machine. That is the same proof the first-run bootstrap already accepts, and no weaker.

import (
	"context"
	"fmt"
	"io"

	"github.com/Coddyum/flowlio-agents/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-agents/internal/pkg/config"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// rotateAdminCommand is the subcommand name: `api rotate-admin`, or, on the compose stack,
// `docker compose run --rm api rotate-admin`.
const rotateAdminCommand = "rotate-admin"

// rotateAdmin revokes every live administration token, issues a new one, and writes it to the
// credentials file the CLI already reads.
//
// The secret is NOT printed, for the reason bootstrapLocal states at length: a subcommand run
// through `docker compose run` leaves its output in the daemon's logs, where a live credential is
// durable and readable by anything that reaches it. Only the path is named.
//
// Hosted mode is refused, and the refusal names where the answer is. There the token is not lost
// in the sense this command repairs: it lives in the deployment's secret store, which the operator
// can read. Rotating it is minting a new one and replacing the environment variable — and doing it
// from here would revoke a credential the co-deployed flowlio-core is holding, mid-flight, without
// telling it.
func rotateAdmin(ctx context.Context, st bootstrap.Store, cfg *config.Config, out io.Writer) error {
	if !cfg.IsLocal() {
		return fmt.Errorf("rotate-admin: hosted mode, where the token comes from ADMIN_TOKEN: " +
			"mint a new one with `mint-admin-token` and replace it in the environment")
	}

	token, revoked, err := bootstrap.RotateAdminToken(ctx, st)
	if err != nil {
		return err
	}

	path, err := credentials.Save(credentials.File{APIURL: apiURL(cfg.Addr), Token: token})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out, "\n  flowlio — admin token rotated")
	_, _ = fmt.Fprintf(out, "  %d previous token(s) revoked. New token stored, never printed.\n", revoked)
	_, _ = fmt.Fprintf(out, "  Credentials: %s (0600)\n", path)
	_, _ = fmt.Fprintln(out, "\n  Every project token stays valid: only the administration ones were replaced.")

	return nil
}
