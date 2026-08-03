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

Requirement: **Docker**. That's all.

```bash
git clone https://github.com/Coddyum/flowlio-agents && cd flowlio-agents
docker compose up -d
docker compose logs api        # prints the admin token, once
```

Three containers start in order: Postgres, migrations, API. The logs print two ready-to-paste
lines:

```bash
export FLOWLIO_API_URL=http://localhost:42058
export FLOWLIO_TOKEN=flw_<prefix>_<secret>
```

Install the CLI from the [latest release](https://github.com/Coddyum/flowlio-agents/releases)
(`flowlio_<version>_<os>_<arch>.tar.gz`), then, **from the root of the repo you want to track**:

```bash
flowlio init --team acme --project API --project-name acme-api
```

This creates the team, the project and an agent token, and writes a `.mcp.json` into the repo. It
prints a new `export FLOWLIO_TOKEN=…` line — that one is the **agent's** token, and it replaces the
admin one.

Your agent can now see flowlio:

```bash
flowlio task create "first task"
flowlio task list
```

Repeat `flowlio init` in your other repo with `--project WEB`, then allow the two to talk:

```bash
flowlio trust allow API WEB
```

> **`.mcp.json` is meant to be committed, and holds no secret.** It references `${FLOWLIO_TOKEN}`,
> which the agent resolves from its environment. A token inside a versioned file is a credential
> published on GitHub — the `.mcp.json` written by `flowlio init` will never contain one, and a
> test asserts it against the file's actual text.

An existing `.mcp.json` is **merged**, never replaced: your other MCP servers survive, and a
hand-tuned `flowlio` entry is left alone.

### Without Docker

```bash
cp .env.example .env          # DATABASE_URL pointing at your Postgres 18
make up-dev                   # migrations (needs golang-migrate)
make run                      # starts the API
```

## The MCP surface — eight tools, on purpose

Every extra tool costs context tokens on **every** agent turn, so the surface is deliberately
small:

| Tool | What it does |
| --- | --- |
| `list_tasks` | this project's backlog |
| `get(ref)` | a task with its notes, or an issue with its thread — untyped on purpose |
| `create_task` | new task, returns its reference |
| `update_task` | status, priority, deadline, body, progress note, archive — one transaction |
| `create_issue` | a question to a sibling repo |
| `list_issues` | the questions exchanged |
| `answer_issue` | reply, and close if that settles it |
| `check_inbox` | what is actionable right now |

**No tool takes a project as a parameter.** The project comes from the token, so there is no MCP
call able to name another repo's backlog.

## Model

```
team (acme)
 └── project (= 1 repo: API, WEB)
      ├── task    ← work internal to the repo
      └── issue   ← question addressed to a sibling repo
```

References are readable — `API-34`, never a UUID. An agent token is scoped to **one project**: it
sees neither other repos' tasks nor other teams.

## What it guarantees

Self-hosted and open source, so the claims have to hold:

- tokens are stored **SHA-256 hashed** — the database holds no reusable secret;
- a secret is shown once at creation, never logged, never shown again;
- **a refused issue is indistinguishable from a project that does not exist** — same status, same
  bytes. Guarded by a test that compares the three refusals byte for byte, not by convention;
- **which repo may write to which is a declared graph** (`flowlio trust allow|deny|list`). The
  check lives in the SQL predicate itself, and a lint rule fails the build if it ever moves into Go;
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

**v0.1.0 — first public release.** The API, the CLI, the MCP server, the trust graph and the inbox
are in and tested. What has *not* happened yet is a long run against a real multi-repo team; that
is the next milestone, and until it does, treat rough edges as expected rather than surprising.

Not built yet: waking a session up when its issue gets answered, versioned decisions and contracts
(the "memory" part), and hosted accounts.

Scope and the reasoning behind each decision: [docs/DESIGN-V1.md](docs/DESIGN-V1.md) (French).

## License

[AGPL-3.0](LICENSE). Self-hosting is free and complete — no feature is held back.
