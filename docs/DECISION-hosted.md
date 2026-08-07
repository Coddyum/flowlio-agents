# DECISION — hosted runs the same engine, operated by flowlio-core

Decided 2026-08-05. Supersedes the `hosted` row of `DESIGN-V1.md` § "Deployment modes",
which announced accounts, JWT and a `billing` module inside this repository. None of that is
happening here.

> **The `D24`–`D30` headings below are not a numbering of their own.** They name the decisions of
> the project-wide register, which is the single authority on what a `D<n>` refers to; this file
> carries their reasoning, at length. Never allocate a new `D<n>` here — a second sequence is how
> two different decisions end up sharing one number.

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
performed server-side instead of from a terminal.

> **Amended 2026-08-07 — the customer no longer reads their token anywhere.** This paragraph ended
> with "the customer reads their token in the web page", and that is no longer what flowlio-core
> does: it mints the project token and **keeps it**. The customer authenticates to flowlio-core,
> names their repository by a non-secret id that is safe to commit, and flowlio-core presents the
> matching token to this API. Nothing here changes — D26 is about who calls the administration API,
> and the answer is still flowlio-core with the operator admin token. What is corrected is only the
> last hop: a token handed over versus a token held. The self-hosted path is untouched —
> `flowlio init` still prints a token, and it is still the only way in on your own machine.

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

> **That sentence describes what shipping D28 removes, not what is removed today — checked
> 2026-08-07.** The embedded SPA does not exist: `embed.go` embeds migrations and nothing else,
> there is no static route in the server, and FLWL-62 is frozen. Until it ships, `ALLOWED_ORIGINS`
> and the private-network preflight are the **only** thing making the self-hosted web screen work,
> because `Flowlio` still calls this API from the browser on master. Removing them first would
> deliver the cost of D28 without its benefit. The preconditions are enumerated in
> `internal/core/engine/cors.go`, and a test holds the door.
>
> An **operated** deployment is not waiting for any of this: it already sets `ALLOWED_ORIGINS` to a
> lone comma, so no browser origin is allowed and the code is inert. Hosted needs configuration,
> self-hosted needs the door — one file serves both, which is why it stays.

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

The MCP server speaks stdio: Claude Code launches `flowlio mcp` as a child process. **A self-hosted
user runs that binary**, which is the point — it is their machine, and the credentials file on it is
what protects them.

Making the same thing true over HTTP requires a transport for the MCP surface. The JSON-RPC layer is
already transport-agnostic (`serve(ctx, in io.Reader)` writing to an `out io.Writer`). The real work
is that tool dispatch currently lives in the CLI and reaches the API over HTTP; serving it from the
API means moving that dispatch server-side.

> **Amended 2026-08-07 — where the hosted MCP endpoint lives, and what authenticates it.** This
> section used to say the hosted transport would be "authenticated by the token this API already
> issues — no OAuth, no accounts, so D24 holds". The endpoint a hosted customer points their agent
> at is **flowlio-core's**, not this one, and flowlio-core authenticates it its own way, with the
> repository named by a non-secret id in the request. This API still sees a project token and
> nothing else.
>
> **D24 holds, and it holds harder than that sentence did.** The reason is not that hosted avoids
> accounts — it does not — but that accounts, and whatever authenticates them, live in
> flowlio-core. Nothing of the sort is added here, ever. An HTTP transport in this repository, if it
> is ever built, is authenticated by the token this API issues, because that is the only credential
> this repository knows.

For a self-hosted user, "nothing to install" was never the promise: they installed on purpose.

## What this deletes

- `DESIGN-V1.md` § "Deployment modes" — the `hosted` row and the `billing` module it announced.
- The reading of FLWL-59 that hosted meant "one flowlio-agents instance per account". It means one
  operated instance, and the tenancy it needs already exists and is proven by mutation (FLWL-31).

## What this raises

A single shared instance means one bug in team scoping leaks between paying customers. Today that
risk is theoretical — one team, ours. Hosted, it becomes the product's main risk. FLWL-44 ("should
the team directory be filtered for a project token") stops being a comfort question and becomes a
security decision.
