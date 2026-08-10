package main

// WHY `flowlio init` IS A SHIM AND NOT A RENAME.
//
// `init --project` meant a REPO KEY. In the language the CLI speaks now, `--project` names a
// project — what the engine calls a team. The same flag, two meanings: `flowlio init --project
// acme` would have quietly created a repository called ACME instead of a project called acme, and
// the failure would have surfaced days later as an agent connected to the wrong board.
//
// So the command stops rather than changes meaning. There is nothing to migrate silently: the two
// commands that replace it cover the interactive path (`setup`) and the scriptable one (`setup
// --yes`, `connect --yes`).

import (
	"context"
	"errors"
)

// runInit prints what replaced this command and fails, so a script that still calls it stops rather
// than half-succeeding.
func runInit(_ context.Context, _ []string) error {
	return errors.New("`flowlio init` is gone: `--project` used to name a repo key, and it now names " +
		"a project.\n" +
		"    flowlio setup            creates the project, its repositories and one token each\n" +
		"    flowlio connect <REPO>   makes one repository operational, run from its root\n" +
		"Both take flags for a non-interactive run, e.g. " +
		"`flowlio setup --project acme --repo API:acme-api`")
}
