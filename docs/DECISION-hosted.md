# DECISION — hosted runs the same engine, operated by flowlio-core

Decided 2026-08-05. Supersedes the `hosted` row of `DESIGN-V1.md` § "Deployment modes",
which announced accounts, JWT and a `billing` module inside this repository. None of that is
happening here.

## The question this answers

Three of the four plans on flowlio.me were sold as *hosted* — "nothing to install, nothing to keep
running" — while the only way in was an admin token printed by a binary running on the user's own
machine. A visitor who paid and installed nothing hit a wall on the first screen after signup.
Tracked as FLWL-59.

## What was decided

### D24 — this repository never carries accounts or billing

No users table, no JWT, no Stripe, no `billing` module. What exists today is the whole auth model:
a token, scoped to a team or to a project. Nothing more is added for the hosted offer.

### D25 — hosted is the same engine, operated by flowlio-core

For a paying customer, **we** run an instance of this exact binary. It is co-deployed inside the
flowlio-core Docker image on the same Render service: flowlio-core listens on Render's port, starts
`flowlio-api` on `127.0.0.1`, and reverse-proxies `/agents/*` to it. One service, one bill, two
databases — the human board keeps its own, the engine gets a dedicated one.

The cost constraint that drove this is real and was stated plainly: the product earns nothing yet,
and a second paid instance is not affordable. Co-deployment answers it without touching the code.

### D26 — flowlio-core is a client of the admin API, not a fork

When a customer subscribes, flowlio-core calls this API with its operator admin token: create the
team, create the project, mint the agent token. That is exactly what `flowlio init` does today,
performed server-side instead of from a terminal. The customer reads their token in the web page.

**No branch, no fork, no copy of the domain.** Co-deployment and code duplication are independent
decisions, and only the first one was needed.

> Consequence on bootstrap: our operated instance still needs exactly one admin token — the
> operator's. `EnsureAdminToken` currently runs only under `cfg.IsLocal()` ([cmd/api/main.go:62]).
> Hosted mode must not disable it; it must change where the secret goes — into a secret store, not
> onto stdout.

### D27 — Postgres only, no SQLite

Offering the choice was considered and refused. 62 lines of the current SQL do not port (`uuid`,
`jsonb`, `timestamptz`, `gen_random_uuid`), 47 named queries would be maintained twice, and every
store integration test would have to run against both engines.

The decisive argument was not cost. **Two engines mean two behaviours** — collation, timezone
handling, case sensitivity in `LIKE`, full-text ranking — between a self-hosted user and a paying
one. That is precisely the divergence D29 forbids.

### D28 — the self-hosted UI is embedded in the binary, same origin

A static SPA (Vite + React), compiled once and embedded with `embed`, served by the HTTP server
that already exists, on the API's own port. Not a Next.js server: Next needs Node at runtime, and
adding a runtime dependency to the product whose install we are simplifying defeats the purpose.

Same origin removes, in one move: the closed origin list, the private-network-access preflight, the
dependency on a Chrome policy that can change without warning, the token pasted by hand, and the
requirement to hold an account on flowlio.me to look at one's own instance — which today is real,
enforced by `Flowlio/src/middleware.ts`, and contradicts "no account, self-hosted".

The embedded bundle and the hosted web UI must remain **one implementation**. Two copies of the
agents view would break parity at the interface instead of the backend.

### D29 — parity is functional, not cosmetic

A feature shipped on one deployment ships on the other, even if it lands a release later. This is
not a matter of discipline: the domain exists once, so it is true by construction.

**Stated exception:** the self-hosted interface may lag the hosted one by days or weeks. That lag
is an *upgrade* lag — the user has not pulled the new binary yet — never a *divergence* of
codebase. One implementation, different release cadence.

### D30 — no TUI in v1

Install friction is real, and it is not solved by a full-screen interface. It is solved by removing
steps: `flowlio init` should start an instance when it finds none, read and write the credentials
file itself instead of asking for a token copied out of `docker compose logs`, and ask at most three
questions. Three questions need `bufio`, not a TUI library. This keeps FLWL-32's ruling intact —
Bubbletea is conditional on the spike and is not bought.

## Still open — it decides what the pricing grid may claim

The MCP server speaks stdio: Claude Code launches `flowlio mcp` as a child process. **A hosted
customer therefore still installs the `flowlio` binary**, and "nothing to install" stays false for
them.

Making it true requires an HTTP transport for the MCP surface, authenticated by the token this API
already issues — no OAuth, no accounts, so D24 holds. The JSON-RPC layer is already transport-
agnostic (`serve(ctx, in io.Reader)` writing to an `out io.Writer`). The real work is that tool
dispatch currently lives in the CLI and reaches the API over HTTP; serving it from the API means
moving that dispatch server-side.

Until that ships, the hosted plans may promise "nothing to **run**" — no database, no backups — and
must not promise "nothing to install".

## What this deletes

- `DESIGN-V1.md` § "Deployment modes" — the `hosted` row and the `billing` module it announced.
- The reading of FLWL-59 that hosted meant "one flowlio-agents instance per account". It means one
  operated instance, and the tenancy it needs already exists and is proven by mutation (FLWL-31).

## What this raises

A single shared instance means one bug in team scoping leaks between paying customers. Today that
risk is theoretical — one team, ours. Hosted, it becomes the product's main risk. FLWL-44 ("should
the team directory be filtered for a project token") stops being a comfort question and becomes a
security decision.
