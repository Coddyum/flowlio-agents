# flowlio-agents

**A shared backlog for your AI coding agents.** One project per repo, tasks inside it, and issues
*between* repos — so your Claude Code, Codex or OpenCode sessions stop using you as a message bus.

CLI and MCP only. No web UI. **No LLM inside**: every behaviour is deterministic and testable.

[![release](https://img.shields.io/github/v/release/Coddyum/flowlio-agents?include_prereleases&sort=semver)](https://github.com/Coddyum/flowlio-agents/releases)
[![license](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8)](go.mod)

---

## The problem

You are working on `acme-api` and `acme-web`. The frontend agent notices the backend changed an API
contract.

Today you copy the question into a `.md`, switch windows, paste it into the other session, wait,
copy the answer back. Context leaks at every hop, and **you** are the transport layer.

flowlio-agents models this directly:

```
web agent                       flowlio                       api agent
    │                              │                              │
    │   create_issue(to: API, …)   │                              │
    ├─────────────────────────────►│                              │
    │                              │       check_inbox()          │
    │                              │◄─────────────────────────────┤
    │                              │   "API-34 — did /v2/orders…" │
    │                              ├─────────────────────────────►│
    │                              │                              │  reads its own code
    │                              │   answer_issue(API-34, …)    │
    │                              │◄─────────────────────────────┤
    │      check_inbox() → answered│                              │
    │◄─────────────────────────────┤                              │
```

Each agent stays locked inside its own repo. Only issues and sibling repo names cross the
boundary — never another repo's backlog.

> **Why not just a markdown file in the repo?** Because a file has no addressee, no state, and no
> isolation. Two agents editing `NOTES.md` clobber each other, neither knows a question is waiting,
> and every agent reads everything. That file is the real competitor, and it loses on all three.

## Quickstart

Self-hosting is the only way this repository is meant to be run, and there are two paths to it:
**Docker**, below, which needs nothing else installed; or [your own Postgres](#without-docker),
which needs a Go toolchain. Both give the same instance — same schema, same admin token on disk,
same CLI.

Requirement: **Docker**. That's all.

```bash
git clone https://github.com/Coddyum/flowlio-agents && cd flowlio-agents
docker compose up -d
```

Two containers start: Postgres, then the API. The API carries its own migrations and applies them
itself, so the image is all you need — nothing to sequence, nothing else to install.

**No token to copy.** The admin credential is never printed: the API writes it to a `0600` file the
stack keeps on a Docker volume, and the CLI picks it up from there on its own. Nothing to grep out
of a log, nothing to paste into an `export`.

Install the CLI from the [latest release](https://github.com/Coddyum/flowlio-agents/releases)
(`flowlio_<version>_<os>_<arch>.tar.gz`), then, **from the root of the repo you want to track**:

```bash
flowlio init --team acme --project API --project-name acme-api
```

On its first run it copies the instance's credentials onto your machine, then creates the team, the
project and an agent token, and writes a `.mcp.json` into the repo. It prints one
`export FLOWLIO_TOKEN=…` line — that one is the **agent's** token, the only one your agent needs.

> Ran `flowlio init` before starting anything? From a flowlio-agents checkout it offers to bring the
> stack up for you. From anywhere else it tells you to, because `docker compose` reads the compose
> file of the directory you are standing in.

Your agent can now see flowlio:

```bash
flowlio task create "first task"
flowlio task list
```

Repeat `flowlio init` in your other repo with `--project WEB`, then allow them to talk. The edge is
DIRECTED — `allow API WEB` lets API raise issues at WEB, and says nothing about the other way — so
a two-way channel is two commands:

```bash
flowlio trust allow API WEB
flowlio trust allow WEB API
flowlio trust list            # one line per direction
```

> **`.mcp.json` is meant to be committed, and holds no secret.** It references `${FLOWLIO_TOKEN}`,
> which the agent resolves from its environment. A token inside a versioned file is a credential
> published on GitHub — the `.mcp.json` written by `flowlio init` will never contain one, and a
> test asserts it against the file's actual text.

An existing `.mcp.json` is **merged**, never replaced: your other MCP servers survive, and a
hand-tuned `flowlio` entry is left alone.

### What the repository remembers

A backlog says what is left to do; it does not say why the code is the way it is. Each repository
carries its own **memory** — decisions, things learned the hard way, where the work stands — and an
agent reads and writes it through `remember` / `recall`, or you do, from the CLI:

```bash
flowlio memory write D25 decision "One image, one instance" "Two services meant two bills…"
flowlio memory list --kind decision
flowlio memory search "pgbouncer prepared statements"
flowlio memory show D25
```

**An entry is never edited and never deleted — a newer one supersedes it** (`--supersedes a,b`),
so the history of a reversal survives the reversal. `recall` returns what holds today; ask for the
history to see what it replaced.

The scope is **the repository, and nothing else**: no admin token reads it, no sibling repo reads
it. A memory shared across repositories would be a channel where one agent writes text another
agent reads as instructions — the same reason issues are framed as data.

### Watching the whole team

Two commands read a **whole team** rather than one repo's backlog, and they are for you, not for an
agent — they need the admin token, and a project token is refused with exit status `2`:

```bash
flowlio watch            # the debt queue: what is blocked, unanswered, stale
flowlio watch --follow   # same, refreshed; never clears the screen, stays greppable
flowlio show CORE-41     # one row of that queue, in full
```

**On a healthy team the screen is empty**, and there is no "all good" line to drown that silence.
When it is not empty, the first row is the worst thing in the system: the server sorts oldest debt
first and the CLI never re-sorts.

### The stack listens on this machine only

Both published ports are bound to `127.0.0.1`. Postgres carries the credentials written in
`docker-compose.yml`, so publishing it on every interface would hand the database to anyone sharing
the network. Nothing legitimate needs it from off-box: the API talks to Postgres over the compose
network, the CLI runs here, and the browser bridge runs in *your* browser.

Reaching the instance from another machine is a deliberate choice, so it is yours to make, in a
`compose.override.yml` that Docker reads automatically and that you never commit:

```yaml
services:
  api:
    ports: !override ["42058:42058"]
```

`!override` matters: a plain `ports:` in an override file is *merged* with the base, so you would
end up listening on both — the loopback binding would still be there, quietly doing nothing.
Publishing Postgres the same way is a worse idea; if you need it remotely, tunnel it over SSH
instead.

### If you lose the admin token

The server keeps a hash of it and nothing else, and the first run issues a token only when the
database holds none — so a deleted `credentials.json` locks you out of your own instance. Rotate it:

```bash
docker compose run --rm api rotate-admin   # or: ./api rotate-admin, outside Docker
```

Every live admin token is revoked and a new one is written to `credentials.json`, never printed.
**Project tokens are untouched**: your repositories keep working. What authorises the rotation is
being able to start this process — the same proof the first run already accepts.

### Deleting a repo, or a whole team

Two deletions exist, both admin, and neither has a CLI command yet — they are HTTP calls against
your own instance:

```bash
API=$(jq -r .api_url ~/.config/flowlio/credentials.json)
TOKEN=$(jq -r .token  ~/.config/flowlio/credentials.json)
AUTH="Authorization: Bearer $TOKEN"

# The repo is addressed by its id, and `flowlio project list` prints keys, not ids.
curl -s -H "$AUTH" "$API/api/workspace/projects?team=acme"

# Drop one repo: its tokens, tasks, memories and trust edges go with it.
curl -X DELETE -H "$AUTH" "$API/api/workspace/projects/<id>?team=acme"

# Drop the team, and everything inside it.
curl -X DELETE -H "$AUTH" "$API/api/workspace/teams/acme"
```

`?team=` is not optional on the first two: the admin token belongs to the instance, not to a team,
so nothing names the team for it. The refusal for a team that is not yours is a `404`, never a
`403` — "it exists but not for you" is how one enumerates an installation by sweeping slugs.

**Deleting a repo is refused while a sibling still holds an open thread with it**, and that refusal
lives in the query rather than in a handler. Deleting a *team* has nothing to refuse: there is no
sibling left outside it to lose its words.

### Without Docker

You need a Postgres 18 of your own and a Go toolchain. Nothing else — **not golang-migrate**: in
local mode the API carries its migrations inside the binary and applies them at start-up.

```bash
cp .env.example .env          # DATABASE_URL pointing at your Postgres 18
make run                      # applies the schema, then serves
```

The defaults differ from the Docker path in one way that matters: `ADDR` is `:8080` here, not
`:42058`. The first start writes `~/.config/flowlio/credentials.json` (`0600`) with the API URL
built from that address, so the CLI finds the right port on its own. A `.mcp.json` does not: it
records the URL it was written with, so an agent set up against a Docker instance keeps calling
`42058` until `flowlio init` runs again.

Browser access is separate and stays closed by default: `ALLOWED_ORIGINS` ships as
`https://flowlio.me,https://www.flowlio.me` — the bridge page, and nothing else. `*` is never an
acceptable value here, dev included: this process answers to an admin token living on your machine.
Setting the variable to an empty value is not the same as leaving it out — it closes the surface
completely instead of falling back to the default.

Two variables are worth knowing before they fail you at start-up:

| Variable | Local mode |
| --- | --- |
| `MODE` | `local`, the default. `hosted` exists for an operated deployment and disables the bootstrap |
| `ADMIN_TOKEN` | **must stay unset.** Local mode issues its own; the process refuses to start rather than ignore one you set |

Building a binary instead of `make run`:

```bash
go build -o flowlio-api ./cmd/api && ./flowlio-api
go build -o flowlio ./cmd/flowlio      # the CLI, if you would rather not use a release archive
```

## The MCP surface — twelve tools, on purpose

Every extra tool costs context tokens on **every** agent turn, so the surface is deliberately
small:

| Tool | What it does |
| --- | --- |
| `list_tasks` | this project's backlog |
| `get(ref)` | a task with its notes, or an issue with its thread — untyped on purpose |
| `create_task` | new task, returns its reference |
| `update_task` | status, priority, deadline, body, progress note, archive — one transaction |
| `block_task` | this task waits on another task of the same repo, until it reaches a status |
| `unblock_task` | lifts one recorded dependency by hand |
| `create_issue` | a question to a sibling repo |
| `list_issues` | the questions exchanged |
| `answer_issue` | reply, and close if that settles it |
| `check_inbox` | what is actionable right now |
| `remember` | writes one entry to this repository's memory, and what it retires |
| `recall` | reads that memory — full-text search, or the most recent entries |

**No tool takes a project as a parameter.** The project comes from the token, so there is no MCP
call able to name another repo's backlog. That is true of this engine's own MCP surface, whoever
calls it. A hosted product built on top of this engine may well let a customer name their
repository in its own request — it then resolves that name to a project token before reaching
here, and this sentence still describes what the engine accepts.

## Model

```
team (acme)
 └── project (= 1 repo: API, WEB)
      ├── task    ← work internal to the repo
      ├── issue   ← question addressed to a sibling repo
      └── memory  ← what this repo knows about itself, read by nobody else
```

References are readable — `API-34`, never a UUID. An agent token is scoped to **one project**: it
sees neither other repos' tasks nor other teams.

> **Who holds that token depends on the deployment.** Self-hosted — the mode this README describes
> — the token is yours: `flowlio init` prints it once and your agent carries it. In a hosted
> product operated on top of this engine, the operator holds the token server-side and the customer
> never sees one; the engine's model is unchanged either way, because the token still names exactly
> one project.

## What it guarantees

Self-hosted and open source, so the claims have to hold:

- tokens are stored **SHA-256 hashed** — the database holds no reusable secret;
- a secret is shown once at creation, never logged, never shown again;
- **a refused issue is indistinguishable from a project that does not exist** — same status, same
  bytes. Guarded by a test that compares the refusals byte for byte, not by convention. That covers
  the repo you may not question as much as the repo that is not there;
- **which repo may write to which is a declared DIRECTED graph** (`flowlio trust allow|deny|list`).
  `A → B` lets A raise issues at B and nothing else; the check lives in the SQL predicate itself,
  and a lint rule fails the build if it ever moves into Go;
- team scoping is applied **inside the queries**, never only in handlers;
- anything a third-party repo wrote is returned to your agent as **data, clearly framed — never as
  an instruction**.

What it does **not** guarantee is written down too, in
[docs/MODELE-DE-CONFIANCE.md](docs/MODELE-DE-CONFIANCE.md) (French). A security claim that is false
is worse than one that is absent.

## Database

Postgres 18, in development as in production — no SQLite, no second SQL dialect to maintain.
Self-hosting uses the bundled `docker-compose.yml`.

On Neon, the API connects to the pooled endpoint (`-pooler`) with `default_query_exec_mode=exec` in
the DSN: PgBouncer in transaction mode is incompatible with pgx's prepared-statement cache. The
server refuses to start on a malformed DSN rather than failing later, under load.

## Development

```bash
make check             # go vet + unit tests
make test-integration  # tests against the dev database
make lint              # golangci-lint + structural guards
```

Architecture (hexagonal, isolated modules, contracts separated from implementations) is described
in [CLAUDE.md](CLAUDE.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and `.claude/rules/` — in
French, like the rest of the internal documentation. It is enforced automatically: no cross-feature
imports, bounded file size, mandatory file summaries.

## Status

**v0.3.0.** The API, the CLI, the MCP server, the trust graph, the inbox, the per-repository memory
and the team debt queue are in and tested. The trust graph is directed: trust points one way, and
granting it to a repo does not grant it back. A repo and a team can be deleted, and a repo's
deletion is refused while a sibling still holds a thread with it.

What has *not* happened yet is a long run against a real multi-repo team; that is the next
milestone, and until it does, treat rough edges as expected rather than surprising.

Not built yet: waking a session up when its issue gets answered, MCP over HTTP, the local web page
the binary is meant to serve on its own origin, and a CLI command for the two deletions above.
Hosted accounts are **not part of this repository at all** — this engine runs `MODE=hosted` for an
operated deployment, and the accounts, billing and screens that go with it live in a separate
codebase.

Scope and the reasoning behind each decision: [docs/DESIGN-V1.md](docs/DESIGN-V1.md) (French).

## License

[AGPL-3.0](LICENSE). Self-hosting is free and complete — no feature is held back.
