# DESIGN v1 — flowlio-agents

Decisions settled from `docs/concept.md`. This document is the contract of what we build in v1;
`docs/ARCHITECTURE.md` remains the technical map of the repo.

## Structuring decisions

| # | Decision | Consequence |
| - | -------- | ----------- |
| 1 | **No board and no columns.** A task carries a `status`. | Flattened `team → project → task` model. A kanban view is reconstituted at read time if a human wants one. |
| 2 | **v1 = tasks + issues + a pull inbox.** | No daemon, no automatic wake-up. The agent calls `check_inbox` itself. |
| 3 | **Memory = decisions + typed contracts** (v2, model already anticipated). | No memory blob and no embeddings. Postgres FTS search + tags. |
| 4 | **Multi-tenant from day one**, `team_id` in every store query. | `local` auth adapter in v1, `hosted` + billing added without touching the stores. |
| 5 | **Zero AI inside the product.** | Every behaviour is deterministic and testable. |
| 6 | **Postgres 18 everywhere, never SQLite.** Production hosted on Neon. | One single SQL dialect, one single set of sqlc queries, no duplicated migration. The install friction in self-hosting is accepted and offset by `docker compose`. |

## Domain model

```
team (tenant)
 └── project (= 1 repo, short key: FRNT, CORE)
      ├── task     ← work internal to the repo, THIS repo's agent handles it
      └── issue    ← cross-project question, within the team only
```

- **task**: `FRNT-34`, `status ∈ {todo, in_progress, blocked, done}`, `priority`, `deadline`, rich
  markdown description, progress notes, archiving.
- **issue**: opened by project A towards project B, `state ∈ {open, answered, closed}`, thread of
  messages. Isolated from B's tasks: answering an issue does not pollute its backlog.
- **event**: append-only journal per team. Feeds `check_inbox` in v1 and will serve as the SSE
  stream for the wake-up daemon in v2 without changing model.

### Identifiers

`<PROJECT_KEY>-<n>` (`FRNT-34`) for tasks and issues. Counter per project, incremented inside the
insertion transaction. Agents never handle a UUID.

## Isolation and permissions

An agent token is scoped to **one project**, within a team. That is the engine's whole
authorisation model, in every deployment: nothing below this line changes when the engine is
operated by somebody else.

> **What DOES change with the deployment is who holds the token, and what the project boundary is
> worth.**
>
> | | Self-hosted | Operated by a hosted product |
> | --- | --- | --- |
> | Who holds the project token | the customer — `flowlio init` prints it once | the operator, server-side; the customer never sees one |
> | How a repository is named | it is not: the token names it | by a non-secret id the caller sends, resolved to a token before reaching this engine |
> | What authorises the caller | possession of the token | the hosted product's own authentication, upstream of here |
> | Between two projects of ONE customer | a **security** boundary — the token is the only key | a **context** boundary: the owner is entitled to both backlogs, and what the boundary buys is that an agent does not read the backlog next door |
> | Between two customers | — | the hosted product's authentication, which is stronger than a secret pasted into a file |
>
> The engine ships the self-hosted column and only that one. The right-hand column describes what a
> client of the administration API may build with it, and is written here so the two claims stop
> contradicting each other across two repositories. See [DECISION-hosted.md](DECISION-hosted.md).

| Resource                        | Scope of the project token     |
| ------------------------------- | ------------------------------- |
| tasks of its own project        | read / write                    |
| tasks of the other projects     | **no access**                   |
| issues it is the sender of      | read / write                    |
| issues it is the recipient of   | read / write                    |
| other issues of the team        | no access                       |
| project metadata of the team    | read-only (key, name)           |
| another team                    | **no access**                   |

The `team_id` + `project_id` filtering is applied **in the store**, never only in the handler: a
query without a scope is a security bug, not a UI oversight.

### The second scoping rule, since `overview`

The `overview` module introduced a **second** form of scope: `team_id` **alone**, read-only, behind
`AdminOnly`. It does not replace the first, it coexists with it — that is the setup where a
re-reading gets it wrong, so the two rules are named and set against each other in
[ARCHITECTURE.md](ARCHITECTURE.md) § The repository's two scoping rules.

What makes it tenable:

- **One single query file carries it** (`sql/queries/overview.sql`), which declares its inverse
  rule at the top. The set of queries able to cross projects is enumerable in one `cat`.
- **Read-only, checked mechanically** — `scripts/check-overview-scope.sh`, in `make lint`.
- **The `team_id` never comes from the client**: it is resolved server-side from the `?team=` slug
  (`OverviewTeamBySlug`), never accepted as a UUID.
- **None of that is exposed over MCP.** An agent reading the state of its team would destroy the
  product's isolation promise, through reads, without a single tenancy test falling over.

The separation of the two rules is held by a test, not by an intention:
`internal/feature/matrix_integration_test.go` (`TestScopeRouteMatrix`) mounts the five modules on
their real stores and crosses three principals — project, admin, none — with the five route
prefixes. A `requireProjectScope` that accepted `|| p.IsAdmin()`, or an `AdminOnly` that accepted a
project scope, makes a cell fall over.

## Deployment modes

> **Revised on 2026-08-05 — see `docs/DECISION-hosted.md`.** This table announced accounts, JWT and
> a `billing` module **in this repository**. That is no longer the plan.

| Mode     | Auth                                                     | Billing |
| -------- | -------------------------------------------------------- | ------- |
| `local`  | No account. `flowlio init` creates team + project + token | —       |
| `hosted` | **Identical.** One admin token, the operator's            | **Elsewhere** |

This repository **never** carries accounts or billing. The hosted offer is that same binary,
operated by flowlio-core: it calls the administration API to create the customer's team, project
and token, exactly as `flowlio init` would from a terminal. The single `Auth()` port of
`CoreServices` stays the one that exists, with a single adapter.

> **Where the customer's token goes, and it is not the same in both modes.** Self-hosted,
> `flowlio init` prints the project token and the customer pastes it into their agent's
> configuration. Hosted, the operator keeps it: flowlio-core mints it through this administration
> API and holds it server-side, so the customer never sees, pastes or stores a token — they name
> their repository by a non-secret id and are authenticated by flowlio-core. The engine is told
> nothing new; the token still names one project, and it is still the only credential this API
> knows. What the customer types is a decision of the product operating the engine, not of the
> engine.

`MODE=hosted` therefore no longer serves to mount a billing module. It decides one thing only:
**where the bootstrap secret goes** — a secret store, never standard output.

## Security (open-source repo — not negotiable)

- Token: `flw_<prefix>_<secret>`, 32 random bytes of secret. Storage **hashed with SHA-256**,
  `prefix` indexed for the lookup. The secret is shown **once only**, at creation. No argon2id
  here: a 256-bit secret has nothing to fear from a dictionary, and a memory-hard KDF on the
  authentication path would be a denial-of-service vector. argon2id is still planned for the
  passwords of hosted accounts (M7).
- Never a token in the logs, the errors, the traces or the issue messages.
- Constant-time secret comparison.
- No secret hard-coded in the binary nor in the repo; `.env` ignored by git, `.env.example` with no
  real value.
- Rate limiting on the token resolution — calibrated below.
- Revocation: `revoked_at`, checked on every request.
- Replay of an interrupted creation: no deduplication, decision argued and verified by execution in
  `docs/DECISION-idempotence.md`.

### Calibrating the rate limiting

Shipped in `internal/core/auth/rate_limit.go`, `trusted_tokens.go` and `request_source.go`.

**What this limiter protects, and what it does not.** It does **not** protect against a token being
discovered: a secret is 32 random bytes, that is 2^256 possibilities, and no threshold changes that
arithmetic — the entropy is what holds. It protects against the **consumption of resources** by a
source failing in a loop: one Postgres round trip and one SHA-256 per attempt.

That distinction commands the rest: since what we defend against is already impossible, any
mechanism able to refuse a **valid** token is a net loss.

**One single bucket**, with a fixed one-minute window, consumed **before** the store round trip —
counting after let a whole concurrent burst through during the database latency.

| Bucket             | Threshold | What it bounds                                          |
| ------------------ | --------- | --------------------------------------------------------- |
| `maxAttemptsPerIP` | 120       | the attempts on distinct tokens from one same source        |

The "source" is not the exact address: in IPv6 it is reduced to its **/64**. The smallest block
assigned to a client is 2^64 addresses — counting the exact address amounted to counting nothing,
an attacker opening a fresh counter on every request.

The threshold is deliberately wide: it bounds a consumption of resources, not a brute force.
Tightening it would refuse legitimate agents starting cold behind one same NAT while gaining
nothing.

**The per-prefix bucket was removed after review.** It existed to slow down relentless attacks on a
specific token. The prefix being the **public** part of the token, it turned out to be the only way
to cut a legitimate agent off: measured, 11 requests a minute on a victim's prefix got their valid
token refused, window after window, and 4,400 requests from a single source cut 400 victims off at
once. In exchange it bought nothing. A device that defends nothing and cuts the legitimate off is
removed, not recalibrated.

**A valid token never consumes quota**, through two mechanisms indexed on the SHA-256 fingerprint
of the **whole token** — never on the prefix:

1. concurrent requests carrying the same token share **one single** charge. A group stops accepting
   new members from its first answer on: without that bound, a pipelined stream kept a group alive
   indefinitely and went through without limit (3,200 requests in 480 ms, measured). The exact
   bound is therefore `maxAttemptsPerIP × concurrency`, and only for requests carrying the same
   token, whose repetition teaches the attacker nothing;
2. a token that authenticated is exempt from quota (24 h), an exemption **withdrawn on the first
   refusal**, which drops a revoked token. An exempt attempt that ends **without a verdict** — a
   client giving up — is charged after the fact: otherwise it was enough to cut the connection to
   never produce a refusal, hence to keep the exemption until the TTL.

This is not an authentication cache: every request still goes to the store and compares the secret,
revocation stays immediate.

**One single outcome gives the charge back: a successful authentication.** Neither the failure, nor
the store outage, nor the client giving up. A previous version also refunded outages, in the name
of availability during an incident; that was a complete bypass, because the attacker brings that
outcome about themselves by abandoning their HTTP request — the cancelled context surfaces as an
outage and refunds the charge the twin request has just paid. The price of that reversal is
bounded: during an incident the API does not answer anyway, and an already authenticated token
stays exempt, so agents in session are untouched — **unless the incident ends in a restart**, the
trust cache living in the process memory.

Known limits, accepted, not compensated elsewhere:

- **the loopback is exempt from the bucket, so the limiter slows nothing down in local mode.** That
  is consistent with the threat model, not an oversight: an attacker able to emit from `127.0.0.1`
  already reads the credentials file, they have no reason to guess a token. Self-hosted, it is the
  filesystem that protects. Corollary: the loopback creates **no** cache key. **Careful**: a
  reverse proxy installed on the same machine as the API makes all traffic look like loopback, and
  therefore disarms the limiter without saying so. For as long as no trusted-proxy configuration
  exists, do not put one in front;

  > **AND THAT IS EXACTLY WHAT AN OPERATED DEPLOYMENT DOES — measured 2026-08-07, FLWL-78.** This
  > paragraph used to end "this limiter defends the hosted mode, where the source of a request is a
  > piece of information". It does not. Co-deployed inside another product's container and reached
  > over `127.0.0.1`, the engine sees a loopback `RemoteAddr` on **every** request of **every**
  > customer, so the per-IP bucket counts nothing at all.
  >
  > Measured, not deduced:
  > `TestCoDeployedTrafficReachesTheLimiterFromTheLoopbackAndIsNotCounted` drives the real
  > middleware at the shipped threshold of 120 and reads the counter — 240 attempts on distinct
  > tokens, counter still 0, a valid token still served; the same sweep from a public address is
  > refused at 121. Turning the exemption off turns it red, which is what makes the reading worth
  > something.
  >
  > **It is not closed by changing who the caller is.** Any caller reached over the loopback gives
  > the same result, and a caller that forwards the real address changes nothing either:
  > `TestForwardedHeadersDoNotRestoreCountingOnTheLoopback` sets `X-Forwarded-For` and `X-Real-IP`
  > and the counter of the forwarded address stays 0. Making the header authoritative goes red on
  > that test — which is the point: it would be a **decision** about what this open-source engine is
  > allowed to assume about its front, not a fix. Options recorded on FLWL-78; none is shipped;
- the blocked path does compute a SHA-256 but does not touch the database: its **latency** tells
  "limited" apart from "refused". Aligning it would mean offering the very query the limiter exists
  to refuse;
- NAT, container or shared proxy: a noisy neighbour can get a valid token refused if it is **not
  yet authenticated** in the current process. To be dealt with the day a trusted proxy exists,
  through explicit configuration, never by trusting `X-Forwarded-For`;
- several instances of the API multiply the effective limit by their number, each carrying its own
  memory counter. The day that happens, it is the cache that changes.

## Schema

Shipped (migrations `000001_init`, `000002_token_scope`, `000003_tasks`):

```
teams(id, slug, name, created_at, updated_at)
projects(id, team_id, key, name, next_number, created_at, updated_at)   unique(team_id, key)
                                                                        unique(id, team_id)
tokens(id, scope, team_id, project_id, name, prefix, secret_hash,
       created_at, last_used_at, revoked_at)                            unique(prefix)
tasks(id, team_id, project_id, number, title, body_md, status,
      priority, deadline, created_at, updated_at, archived_at)          unique(project_id, number)
task_notes(id, task_id, body_md, created_at)
```

`tokens.scope` is `admin` (local bootstrap, no team) or `project` (agent, team and project
mandatory) — one single table, hence one single secret-verification path.

`tasks` carries a **denormalised** `team_id` so that every query can embed its full tenancy scope
without a join. The composite foreign key `(project_id, team_id)` towards `projects (id, team_id)`
— hence the uniqueness added on `projects` — guarantees that this denormalisation can never
diverge: a task whose `team_id` lies is impossible in the database, not merely improbable. The same
schema will apply to the issues.

`task_status ∈ {todo, in_progress, blocked, done}`,
`task_priority ∈ {low, normal, high, urgent}` (default `normal`).

Also shipped (migration `000004_issues`):

```
issues(id, team_id, project_id, author_project_id, number, title,
       state, created_at, updated_at, closed_at)                   unique(project_id, number)
issue_messages(id, issue_id, author_project_id, body_md, created_at)
events(id, team_id, project_id, actor_project_id, kind, subject_type, subject_id, created_at)
token_cursors(token_id, last_event_id, updated_at)
```

`events` carries an `actor_project_id` the announced model did not have, and **check_inbox does not
read that journal as a stream**: it yields the current actionable state, and the cursor only serves
the "new" flag. Full reason in [DESIGN-M3.md](DESIGN-M3.md) — that is what makes the sequence gap
of a `bigserial` counter inconsequential, instead of a class of bugs.

An issue belongs to the **recipient** project (`project_id`) and remembers its author
(`author_project_id`), like a GitHub issue belongs to the repo it is opened on. It draws its number
from that project's counter: tasks and issues share the same sequence, so `CORE-34` always
designates one single object.

Consequence on the MCP surface: `get(ref)` is not typed. An agent reading `CORE-34` in a commit or
in its inbox does not know whether it is a task or an issue — two typed tools would fail one time
out of two.

## Split into modules (`internal/feature/`)

| Module      | Key         | Responsibility                                              | State |
| ----------- | ----------- | ----------------------------------------------------------- | ---- |
| `workspace` | `workspace` | teams, projects, agent tokens (creation, revocation)         | shipped |
| `task`      | `task`      | a project's tasks + progress notes + archiving               | shipped |
| `issue`     | `issue`     | cross-project issues, message thread, state changes          | shipped |
| `inbox`     | `inbox`     | actionable state of the project (three buckets) + token cursor | shipped |

`auth` is not a feature: it is a cross-cutting service of `internal/core`, exposed through
`CoreServices.Auth()` (token → `Principal{TeamID, ProjectID}` resolution + middleware).

Expected inter-module dependency: `issue` and `task` emit events consumed by `inbox`. Goes through
the `FeatureRegistry`, never through a direct import — or through an `EventWriter` port on the
`internal/store/` side if the write must be transactional with the task/issue.

## MCP surface (v1)

Small by design: every superfluous tool costs tokens on **every turn** of an agent.

Surface really served (M2 → M3, tightened by FLWL-15). **Eight tools**, settled in
`docs/DESIGN-M3.md`: that file is what counts on the MCP surface.

```
list_tasks(status?, limit?, archived?)       → backlog of the current project
get(ref)                                     → task + note thread, or issue + message thread
create_task(title, body?, ...)               → new task, yields its reference
update_task(ref, ..., note?, archive?)       → status, priority, deadline, description,
                                               progress note, archiving
```

`whoami` is not a tool: its content is constant over the life of the token, it is injected into
`initialize.instructions`. `get_task` is absorbed by `get(ref)` — tasks and issues share the
project's counter, so a typed tool would fail one time out of two. `add_task_note` is folded into
`update_task(note:)`, written in the same transaction as the patch.

Shipped in M3:

```
create_issue(to_project, title, body)        → question to a sibling project
list_issues(role?, state?, limit?, closed?)  → the questions exchanged
answer_issue(ref, body, close?)              → answer, and close if asked
check_inbox()                                → the current actionable state
```

`close_issue` never existed: it is the `close` flag of `answer_issue`, because the majority case is
"I answer and that closes the subject".

> `check_inbox` does NOT yield "the events since the cursor" as this section announced before M3.
> It yields the **current actionable state**, recomputed on every call; the cursor only drives the
> `new` flag. The reason is in `docs/DESIGN-M3.md` — a stream would require exactly-once delivery
> that `events.id` cannot guarantee, and a lost issue would be lost forever.

**No tool accepts a project as a parameter** (except `to_project` of an issue, which designates a
recipient and not a read scope): the project comes from the token. There is therefore no MCP call
able to designate another project's backlog.

> That is a statement about **this** MCP surface — the one `flowlio mcp` serves, over stdio, on the
> project token it was given. A hosted product may expose its own MCP endpoint where the customer
> names their repository; the name is resolved to a project token upstream, and what reaches these
> tools is still a token and no project parameter. The rule above is therefore not weakened by such
> a surface — it is what makes it safe to build one.

`archive_task` and `add_task_note` were merged into `update_task`, as an `archive` flag and a
`note` field. The same reason in both cases: one more tool is paid for in the context of every
turn, for actions nobody calls without changing a status in the same move. And for the note, the
folding closes a state of affairs: the patch and the note now share a transaction, so "status
changed, reason lost" is no longer reachable.

Every write return has the same shape, `{ref, task}` or `{ref, issue}`: the agent reads the
reference in the same place whichever tool it has just called.

## Binaries

- `cmd/api` — HTTP server (existing). It is what a hosted product operates; it is the whole engine.
- `cmd/flowlio` — human CLI (`init`, `project`, `token`, `task`, `issue`) **and** stdio MCP server
  through `flowlio mcp`. Same binary, same auth, same HTTP client.

> **The CLI is the self-hosted tool.** It exists to stand up an instance and hand a token to an
> agent on the machine that runs it — `flowlio init`, `FLOWLIO_TOKEN`, stdio. A customer of a
> hosted product does not install it and has no token to give it. This was once written as "local
> and hosted do not diverge"; what does not diverge is the **domain**, served by one `cmd/api` in
> both cases. The way a human or an agent reaches that domain does diverge, and pretending
> otherwise is what left two repositories claiming different things.

## Out of scope for v1 (accepted)

- Automatic wake-up of the sessions (local daemon + SSE) — the event journal is already there to
  host it.
- Versioned decisions / contracts — model planned, not implemented.
- Hosted accounts, JWT, Stripe.
- Any web interface.
