package main

// THE ONE PLACE A TOKEN IS PRINTED, and the exception is narrow enough to state in a sentence.
//
// Everywhere else the rule holds: a secret this server issues reaches a 0600 file and nothing else,
// because `docker compose run` and a container's start-up both end in a log that outlives the
// command and is readable by anything that reaches the daemon.
//
// This command issues a token and touches NO DATABASE. Nothing has been registered when it returns,
// so what it prints is not yet a credential of any installation — it becomes one only when the
// operator puts it into their deployment's secret store and the server registers its hash on the
// next start. There is no other channel: the secret has to reach a human to be pasted, and this
// binary is where the token format lives.
//
// Stdout carries the token ALONE, with no banner and no trailing advice, so that
// `ADMIN_TOKEN=$(flowlio-api mint-admin-token)` is a correct thing to write. Guidance belongs in
// the deployment documentation, where it can be read twice.

import (
	"fmt"
	"io"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

// mintAdminTokenCommand is the subcommand name: `flowlio-api mint-admin-token`.
const mintAdminTokenCommand = "mint-admin-token"

// mintAdminToken prints one freshly generated administration token and nothing else.
func mintAdminToken(out io.Writer) error {
	token, err := crypto.NewToken()
	if err != nil {
		return fmt.Errorf("mint-admin-token: %w", err)
	}
	if _, err := fmt.Fprintln(out, token.Plain); err != nil {
		return fmt.Errorf("mint-admin-token: %w", err)
	}
	return nil
}
