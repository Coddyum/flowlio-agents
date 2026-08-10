# DECISION — the self-hosted setup is two commands, and no secret is ever pasted

Decided 2026-08-10. Supersedes the quickstart of `README.md` as it stood, and retires
`flowlio init`.

> **No `D<n>` heading here.** The project-wide register is the single authority on what a `D<n>`
> refers to, and a second sequence is how two decisions end up sharing one number. These entries
> belong in the `memory` register (`remember`, with `supersedes`) and are written down here because
> `docs/decisions.md` is **gitignored** — a decision filed only there travels nowhere.

## The question this answers

Setting up a self-hosted deployment meant three installations, a secret pasted by hand and no
accompaniment, where the hosted product asks for an account and four "copy" buttons. What was
actually in the code, not in the documentation:

- both repositories' `.mcp.json` referenced the same `${FLOWLIO_TOKEN}`, so the second repository
  on a machine took a 401 and nothing said why;
- there was no workflow prompt, no pointer to one, and no inbox hook — all three exist for hosted
  customers, in `flowlio-core` and `Flowlio`;
- the README still asked for two `flowlio trust allow` commands, which the directed edges written
  at repo creation had made obsolete;
- nothing verified anything afterwards: the hosted product has a screen, the self-hosted path had
  nothing;
- `.mcp.json` froze the API address, so a repository initialised against Docker called `:42058`
  forever.

## What was decided

### The CLI speaks the product's language, not the engine's

A `project` in the CLI is what the engine calls a **team**; a `repo` is what the engine calls a
**project**. The translation happens at the boundary, and a CLI is a boundary.

The engine's own commands (`team`, `project`, `token`, `trust`) keep the engine's words: they are
administrative surfaces over the API, and renaming them would leave the API and the CLI disagreeing
about what a word means.

### The project token leaves the environment

It lives in `$XDG_CONFIG_HOME/flowlio/repos/<project>/<REPO>.json`, `0600`, one file per
repository. The `.mcp.json` carries two NAMES — `FLOWLIO_PROJECT` and `FLOWLIO_REPO` — and the MCP
server resolves them.

One variable name could not serve two repositories on one machine, and per-directory exports do not
reach an agent launched from an editor. The address travels with the token for the same reason: as
host-local state it can be rewritten by `flowlio connect`, where a committed file could only drift.

### `flowlio init` dies rather than changes meaning

`init --project` named a repo key. In the new language it would name a project, so
`flowlio init --project acme` would have quietly created a repository called `ACME`. The command
is a shim that prints what replaced it and exits 1. The scriptable path is covered by
`setup --project … --repo …` and `connect --yes`.

### The workflow prompt is written to a neutral path, and pointed at

`.flowlio/workflow.md` — not under `.claude/`, not under `.cursor/`. Several clients can be used in
one repository, and a prompt filed under one of their directories reads as belonging to it.

**The prompt never goes into the entry file.** Two hundred and fifty lines inside a file loaded on
every session drown the repository's own doctrine; but a rules file nothing points at is a file
nothing opens. Neither half works alone. The reasoning is `flowlio-core`'s, in
`Flowlio/src/lib/agents/claude-md-pointer.ts`, and it is taken as it stands.

The markdown is a copy of `flowlio-core/internal/feature/agents/prompt/flowlio-workflow.md`. **The
engine is the canonical home** — it owns the twelve tools the text describes — and having
`flowlio-core` consume this package is a card, not a regret. The version number is what makes the
drift visible from both sides.

### Everything written into a user's file is bounded by markers

`<!-- flowlio:start -->` / `<!-- flowlio:end -->` buy two properties, and both are load-bearing:
re-running `connect` REPLACES what is between them rather than stacking a second copy, and
`flowlio disconnect` lifts the block back out. Without them, disconnecting means telling a human to
edit the repository's own doctrine file by hand.

Text files come back byte for byte. The two JSON files are decoded and re-encoded, so the first
`connect` normalises their indentation once; nothing is lost and a second cycle changes nothing.

### Detection is confirmed per client, and unconfirmed means not implemented

A pointer written where nobody reads it is a silent failure. Each signal was checked against the
client's own documentation before being implemented — Claude Code's `CLAUDE.md`, the open
`AGENTS.md` format, Cursor's `.cursor/rules/*.mdc` (the extension is required and the front-matter
carries `alwaysApply`), GitHub's `.github/copilot-instructions.md`. Anything unconfirmed falls
through to the printed fallback, which is correct by construction.

### The self-test is the tail of `connect`, not a command of its own

A separate `verify` is a command nobody runs, and the moment verification is worth anything is the
moment the files have just been written. `flowlio doctor` replays the same ground later, from a
repository that is already connected, and reports on every check rather than stopping at the first.

### `connect` needs the machine that runs the instance, and says so

It issues the repository's token, which needs the admin credential. A teammate who clones the
repository elsewhere gets the `.mcp.json` and not the token. Acceptable for a single-operator
self-hosted deployment — and printed in `setup`'s output rather than left to be discovered.

## What was explicitly left out

- **MCP over HTTP.** Out of scope, but the `.mcp.json` entry is composed in one function
  (`flowlioEntry`) so the transport is swappable the day it lands.
- **A published Docker image and an installer.** They will get their own decision.
