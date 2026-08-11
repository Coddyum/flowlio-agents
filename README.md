# flowlio-agents

**A shared backlog for your AI coding agents.** One project per repo, tasks inside it, and issues
*between* repos — so your Claude Code, Codex or OpenCode sessions stop using you as a message bus.

And when a sibling repo answers a question, **the agent that asked wakes up and reads the answer on
its own** — the session can die in between, and the loop still closes with no human in the middle.
That is what v1.0.0 adds; see [Waking a dead session](#waking-a-dead-session).

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

---

## The three moving parts

Read this once and the rest of the README stops being surprising. Everything runs on **your**
machine; nothing calls out to us, and there is no account anywhere.

```
   ┌───────────────────────────────────────────────────────────────┐
   │ your machine                                                  │
   │                                                               │
   │  ① the instance                    ② the CLI                  │
   │  ┌────────────┐  ┌──────────┐      ┌──────────────┐           │
   │  │ flowlio-api│──│ Postgres │      │  flowlio     │  you type │
   │  │  :42058    │  │  :5433   │      │  (one binary)│  these    │
   │  └────────────┘  └──────────┘      └──────────────┘           │
   │        ▲   two containers               │                     │
   │        │   `docker compose up -d`       │ writes tokens into  │
   │        │                                ▼ ~/.config/flowlio/  │
   │        │                                                      │
   │        │   ③ the MCP server: `flowlio mcp`, over stdio        │
   │        └──────────────── started BY your agent client ────────┤
   │                          (Claude Code, Codex, …) — never by you│
   └───────────────────────────────────────────────────────────────┘
```

| # | What | Who starts it | How often |
| --- | --- | --- | --- |
| ① | the instance — API + Postgres, in Docker | you, once | survives reboots on its own |
| ② | the `flowlio` CLI | you, when you want to look at something | never in the background |
| ③ | the MCP server (`flowlio mcp`) | your agent client, from `.mcp.json` | automatic, per session |

**There is no daemon of ours to babysit, and nothing to launch every morning.** See
[Everyday use](#everyday-use) for the whole answer.

> **One optional fourth part, and it is yours too:** the **waker**. It is what relaunches an agent
> when a sibling answers it, and it is a mode of the same `flowlio` program — `flowlio waker`, or
> just `flowlio`, which brings up everything at once. Nothing of ours runs off your machine. See
> [Waking a dead session](#waking-a-dead-session).

---

## Setup

The fast path, once the Homebrew tap is published:

```bash
brew install flowlio
flowlio                 # brings up Postgres (in a container it manages) + the engine + the waker
```

One program, one command. `flowlio` runs the whole self-host stack in one terminal; `flowlio help`
prints the surface. Then connect a repository with `flowlio connect <REPO>` and pick its agent with
`flowlio agent set claude|codex|opencode`.

The rest of this section is the **explicit** path — `docker compose` for the instance and a release
binary for the CLI — which is what `flowlio` automates and what you want when you are wiring it into
an existing stack. Four steps, ten minutes, and the fourth one is the check that the first three
worked.

Requirement: **Docker**. That is the entire prerequisite list. (Prefer your own Postgres and a Go
toolchain? See [Without Docker](#without-docker) — you get the same instance either way.)

### 1. Start the instance

```bash
git clone https://github.com/Coddyum/flowlio-agents && cd flowlio-agents
docker compose up -d
```

Two containers start: Postgres, then the API. The API **carries its own migrations** and applies
them at start-up, so there is no third step to sequence, nothing to install, and no `migrate`
binary to find. The first run builds the image and takes a minute; later ones are instant.

Check it came up:

```bash
docker compose ps          # both services "running", postgres "healthy"
```

**No token to copy.** The admin credential is never printed: the API writes it to a `0600` file the
stack keeps on a Docker volume, and the CLI picks it up from there on its own. Nothing to grep out
of a log, nothing to paste into an `export`.

### 2. Install the CLI

Download `flowlio_<version>_<os>_<arch>.tar.gz` from the
[latest release](https://github.com/Coddyum/flowlio-agents/releases), then:

```bash
tar xzf flowlio_<version>_<os>_<arch>.tar.gz
sudo install flowlio /usr/local/bin/
flowlio version           # says which release you are on — paste this into any bug report
```

Verify the download first if you like: `sha256sum -c checksums.txt --ignore-missing`.

<details>
<summary>Building it yourself instead (needs Go 1.26)</summary>

```bash
go build -o flowlio ./cmd/flowlio && sudo install flowlio /usr/local/bin/
```

A binary built this way reports `flowlio dev`, which is the honest answer: a checkout is not a
release.
</details>

### 3. Create a project, and connect each repository

```bash
flowlio setup
  Project name?  acme
  Repo key?      API      Repo name? acme-api
  Add another repo? [y/N] y
  Repo key?      WEB      Repo name? acme-web
  Add another repo? [y/N] n
```

A **project** is your team or product; a **repo** is one git repository with one agent working in
it. `setup` creates both on the instance, issues one token per repository and files each one in
`~/.config/flowlio/repos/` (`0600`). **No secret is printed and none goes into a repository.**

It ends on one line per repo, to be run from that repository's root:

```bash
cd ../acme-api && flowlio connect API
cd ../acme-web && flowlio connect WEB
```

Closed the terminal before copying them? `flowlio setup --list` reprints them from what is already
filed on this host.

Scriptable form, for when you would rather not answer questions:

```bash
flowlio setup --project acme --repo API:acme-api --repo WEB:acme-web
flowlio connect API --yes
```

`connect` writes four things, and says so before touching any of them:

| File | What it is | Asked first? |
| --- | --- | --- |
| `.mcp.json` | the MCP server entry, merged into whatever is there | no, it is ours |
| `.flowlio/workflow.md` | how an agent is meant to work with Flowlio | no, it is ours |
| `CLAUDE.md`, `AGENTS.md`, `.cursor/rules/`, Copilot instructions | three lines pointing at the file above, in whichever of them your repository shows signs of | yes |
| `.claude/settings.json` | a throttled reminder to read the inbox, only if `.claude/` exists | yes |

Both blocks written into your own files are bounded by `<!-- flowlio:start -->` markers, so a second
`connect` replaces them instead of stacking, and `flowlio disconnect` takes them back out. Say no,
or run it with no terminal, and it prints exactly what it would have written.

It ends by checking itself: the instance answers, the token is accepted, it belongs to the repo the
`.mcp.json` names, and twelve tools are offered. Nothing green is announced without having been
observed.

### 4. Restart your agent client, then check

**An agent client reads `.mcp.json` when it starts, and not again.** A session that was already
open when you ran `connect` will not see Flowlio — quit it and reopen it in that repository. Claude
Code will also ask you to approve the new MCP server the first time; approve it, or nothing is
loaded.

Then, from the repository:

```bash
flowlio doctor
```

It replays the same ground `connect` covered — instance reachable, credential readable, token
accepted, right repo, workflow file current — and reports on **every** check rather than stopping
at the first. Somebody runs this because they do not know what is wrong, and three red lines locate
a problem one red line does not.

Ask your agent to run `list_tasks`, or check by hand:

```bash
flowlio task create "first task"
flowlio task list
```

**The two repositories can already write to each other.** A repo arrives connected: creating it
opens a trust edge to and from every repo already in the project, so `create_issue` works at the
first gesture. `flowlio trust list` shows one line per direction, and `trust deny` closes one.

> **`.mcp.json` is meant to be committed, and holds no secret.** It carries two names —
> `FLOWLIO_PROJECT` and `FLOWLIO_REPO` — and the CLI resolves them into an address and a token that
> never left your machine. It does not even carry a `${FLOWLIO_TOKEN}` reference any more: one
> variable name could not serve two repositories on one machine, and the second one set up took a
> 401 with nothing to say why. A test asserts the absence against the file's actual text.

An existing `.mcp.json` is **merged**, never replaced: your other MCP servers survive, and a
hand-tuned `flowlio-agents` entry is left alone.

> **`flowlio connect` runs on the machine that runs the instance.** It reads the admin credential to
> issue a token, and that credential lives here. A teammate who clones the repository elsewhere gets
> the `.mcp.json` and not the token — their agent says so, and names the command.

---

## Everyday use

The short version: **once it is set up, you run nothing.**

| Situation | What you do |
| --- | --- |
| every morning | nothing |
| after a reboot | nothing — as long as Docker itself starts (see below) |
| starting an agent session | nothing; your client launches `flowlio mcp` from `.mcp.json` |
| wanting answers to wake the agent back up | run `flowlio waker` (or `flowlio`, which includes it) |
| adding a repo to an existing project | `flowlio project create <KEY> <name>` then `flowlio connect <KEY>` in it |
| looking at the whole team | `flowlio watch` |
| something feels off | `flowlio doctor` |

The one thing you *may* choose to keep running is the **waker** — the process that relaunches an
agent when a sibling answers it. It is opt-in, it is yours, and it runs nothing off your machine;
[Waking a dead session](#waking-a-dead-session) is the whole of it.

**Why nothing after a reboot.** Both containers carry `restart: unless-stopped`, so Docker brings
them back by itself when the daemon starts. The only link in that chain that is yours is Docker:
on macOS and Windows, Docker Desktop must be set to open at login (Settings → General → *Start
Docker Desktop when you sign in*); on Linux, `systemctl enable docker` does the same. If the
instance is down, everything says so plainly rather than hanging — `flowlio doctor` names it, and
an agent's tool call comes back as a refusal it can read.

Starting and stopping it by hand, from the `flowlio-agents` checkout:

```bash
docker compose ps        # is it up?
docker compose stop      # stop, keeping everything
docker compose start     # back up
docker compose logs -f api   # what the API is doing
```

`docker compose down` also removes the containers; your data survives, because it lives in a volume
(next section). **`docker compose down -v` is the one that deletes it.**

**You never run `flowlio mcp` yourself.** It is the MCP server, it speaks over stdin/stdout, and it
is started by your agent client on demand from the `.mcp.json` entry. Typing it into a terminal
gets you a process waiting for JSON-RPC that will never come.

---

## Waking a dead session

An agent is an ephemeral process — it cannot sit and listen. So when repo B answers a question repo
A asked, the session in A that asked is already gone, and nobody learns the answer until you relaunch
it by hand. The **waker** removes that last human step.

```bash
flowlio agent set claude       # or codex, opencode, or set-custom "<your tool> {prompt}"
flowlio waker                  # watches every connected repo; relaunches its agent when answered
```

`flowlio waker` (or just `flowlio`, which starts it alongside the engine) watches for answers and,
when one lands for a repo, launches that repo's agent **in that repo's directory** to read it. For
Claude it re-enters the exact session that asked (`claude -r`), carrying its context; for any other
agent — or Claude with no live session — it starts a fresh one that rebuilds context from
`check_inbox`. You pick per repo with `flowlio agent set`.

**It is built not to cost you.** "Is there anything for me?" is an integer comparison held in memory —
never a database query — so an idle repo is watched for free; only "what is it?" touches Postgres,
and only when the first says yes. The server dictates how often the waker may ask, and a relaunch
cap turns two repos answering each other into a bounded burst instead of a runaway loop. The cost
follows real events, never time × agents.

**Self-host** and it needs no configuration beyond `flowlio agent set`: the engine is on the same
machine and pushes to the waker on `127.0.0.1` the instant an event drops, with a secret so no other
process can trigger a relaunch. **Hosted** — the engine runs on our infra behind a NAT it cannot
push through — the waker polls instead, after one `flowlio login`, and your agent's code and
credentials never leave your machine.

> Want to watch the loop close before trusting it? `scripts/demo-wake.sh` stands the whole thing up
> on your machine — engine and waker as real processes — and proves an answer relaunches the agent
> with no human in the middle.

---

## Where your data lives

Everything is in Postgres, in a container, on your machine. Three locations, and that is the
complete list:

| What | Where | Removed by |
| --- | --- | --- |
| tasks, issues, memory, trust edges, token hashes | Docker volume `flowlio-pgdata` | `docker compose down -v` |
| the instance's admin credential, container side | Docker volume `flowlio-config` | `docker compose down -v` |
| your copy of the admin credential + one token per repo | `~/.config/flowlio/` (`0700`, files `0600`) | `rm -rf ~/.config/flowlio` |

`$XDG_CONFIG_HOME` wins over `~/.config` if you set it — on macOS too, deliberately: the CLI does
not scatter itself into `~/Library/Application Support`.

**The database.** Postgres 18, user/password/database all `flowlio`, published on
`127.0.0.1:5433` so it cannot collide with a Postgres you may already run on 5432. The API talks to
it over the compose network, not through that published port; the port is there for you, to run
`psql` against it.

**The schema.** Migrations are compiled into the API binary (`embed.go`) and applied at start-up in
local mode. There is nothing to run, no `migrate` to install, and upgrading the image upgrades the
schema. In an operated deployment (`MODE=hosted`) the API does the opposite: it checks the schema
and **refuses to serve** on one it does not match, rather than migrating on its own — a redeploy
must never change a schema without someone having decided so.

**Backing it up.** One command, and the dump is a plain SQL file:

```bash
docker exec flowlio-postgres pg_dump -U flowlio -d flowlio > flowlio-backup.sql
```

Restoring it into a fresh instance:

```bash
docker exec -i flowlio-postgres psql -U flowlio -d flowlio < flowlio-backup.sql
```

A backup of the database alone is enough to keep your boards. It does **not** contain any usable
token — tokens are stored SHA-256 hashed — so after a restore, re-issue them with
`flowlio connect <REPO>` in each repository.

**Looking at it directly**, if you want to:

```bash
docker exec -it flowlio-postgres psql -U flowlio -d flowlio
```

---

## Upgrading

```bash
cd flowlio-agents
git pull
docker compose up -d --build      # rebuilds the API, applies any new migration at start-up
```

Then install the matching CLI from the [releases](https://github.com/Coddyum/flowlio-agents/releases)
page and re-run, in each connected repository:

```bash
flowlio doctor        # says if the workflow prompt is at an older version
flowlio connect <REPO>   # rewrites it if it is
```

The workflow prompt written into `.flowlio/workflow.md` carries a version number in its heading, so
a repository holding an old one is visible rather than merely stale. `connect` replaces a file at
another version and leaves a current one alone.

**Keep the CLI and the instance on the same release.** They are published together, in the same
archive, for that reason.

---

## Uninstalling

```bash
flowlio disconnect                       # in each repository — no network call, no instance needed
docker compose down -v                   # deletes both volumes: THE DATABASE GOES WITH THEM
rm -rf ~/.config/flowlio                 # your admin credential and repo tokens
sudo rm /usr/local/bin/flowlio           # the CLI
```

`docker compose down -v` is irreversible and takes every task, issue and memory entry with it. Take
a `pg_dump` first if you might want any of it back.

`flowlio disconnect` lifts the blocks bounded by the `flowlio:start` markers out of your own files
and removes ours; it does not touch the instance. To also drop the repo server-side, see
[Removing things](#removing-things).

---

## What you can do from the CLI

Everything an agent can do through MCP, plus the administration an agent has no business doing.

```bash
flowlio task list [--status s]     # backlog of this repository
flowlio task show API-34           # one task and its note thread
flowlio task create "title"
flowlio task status API-34 done    # todo | in_progress | blocked | done
flowlio task note API-34 "text"
flowlio task archive API-34

flowlio whoami                     # which token am I using, and for what
flowlio project list               # the repos of the project
flowlio trust list                 # who may raise issues at whom, one line per direction
flowlio doctor                     # is this repository going to work
flowlio version

flowlio                            # run everything: engine + waker (self-host), waker only (hosted)
flowlio waker                      # just the waker: relaunch agents when a sibling answers
flowlio agent set claude|codex|opencode   # which agent the waker launches for this repo
flowlio login <prod-url>           # hosted: link this machine to your flowlio.me account
```

`flowlio help` prints the whole surface, including the `team` / `project` / `token` commands, which
speak the engine's words rather than the product's — a *team* there is what `setup` calls a project,
and a *project* is what it calls a repo. The translation happens at the CLI boundary and stops
there.

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

### Removing things

```bash
flowlio disconnect                # this repository only, no network call
flowlio remove API                # the repo on the instance; refused while a sibling holds a thread
flowlio remove --project acme     # the project and everything in it, after retyping the slug
```

**Deleting a repo is refused while a sibling still holds an open thread with it**, and that refusal
lives in the query rather than in a handler. Deleting a *project* has nothing to refuse: there is no
sibling left outside it to lose its words.

The same two deletions over HTTP, if you would rather script them against your own instance:

```bash
API=$(jq -r .api_url ~/.config/flowlio/credentials.json)
TOKEN=$(jq -r .token  ~/.config/flowlio/credentials.json)
AUTH="Authorization: Bearer $TOKEN"

# The repo is addressed by its id, and `flowlio project list` prints keys, not ids.
curl -s -H "$AUTH" "$API/api/workspace/projects?team=acme"
curl -X DELETE -H "$AUTH" "$API/api/workspace/projects/<id>?team=acme"
curl -X DELETE -H "$AUTH" "$API/api/workspace/teams/acme"
```

`?team=` is not optional on the first two: the admin token belongs to the instance, not to a team,
so nothing names the team for it. The refusal for a team that is not yours is a `404`, never a
`403` — "it exists but not for you" is how one enumerates an installation by sweeping slugs.

### If you lose the admin token

The server keeps a hash of it and nothing else, and the first run issues a token only when the
database holds none — so a deleted `credentials.json` locks you out of your own instance. Rotate it:

```bash
docker compose run --rm api rotate-admin   # or: ./flowlio-api rotate-admin, outside Docker
```

Every live admin token is revoked and a new one is written to `credentials.json`, never printed.
**Project tokens are untouched**: your repositories keep working. What authorises the rotation is
being able to start this process — the same proof the first run already accepts.

---

## Troubleshooting

Run `flowlio doctor` first, from the repository in question. Almost everything below is a line it
prints.

**My agent does not see Flowlio at all.**
The client read `.mcp.json` at start-up and has not read it since. Quit it, reopen it in that
repository, and approve the server if it asks. `flowlio doctor` passing while the agent sees
nothing is exactly this.

**`no credentials found — run flowlio setup`.**
No `~/.config/flowlio/credentials.json` on this host. If the instance is running, any command will
adopt the credential from the container by itself; if it is not, start it with `docker compose up -d`
from the `flowlio-agents` checkout.

**`the instance answers at …` fails.**
The containers are down, or Docker is. `docker compose ps` from the checkout, then
`docker compose up -d`. If the API container restarts in a loop, `docker compose logs api` names the
cause on its first lines — a bad `DATABASE_URL` and an `ADMIN_TOKEN` set in local mode are the two
that stop it on purpose.

**A second repository gets 401 while the first works.**
That was the `${FLOWLIO_TOKEN}` era. Re-run `flowlio connect <REPO>` in it: the token moves into
`~/.config/flowlio/repos/`, one file per repository, and the `.mcp.json` stops carrying an
environment reference.

**My agent calls the wrong address / a stale port.**
An old `.mcp.json` froze the API address. `flowlio connect <REPO>` rewrites it; addresses now travel
with the token in `~/.config/flowlio/repos/`, which is host-local and rewritable.

**`create_issue` is refused.**
Either the trust edge is closed in that direction, or the target repo does not exist — and the two
refusals are **byte for byte identical**, on purpose. Check with `flowlio trust list`, and open one
with `flowlio trust allow <from> <to>`. Trust is directed: `A → B` does not grant `B → A`.

**`flowlio watch` exits with status 2.**
It needs the admin token and you are running under a project token. Run it outside a connected
repository, or unset `FLOWLIO_TOKEN`.

**Port 5433 or 42058 is already taken.**
Change the host side of the mapping in a `compose.override.yml` (Docker reads it automatically);
if you move the API port, re-run `flowlio connect <REPO>` in each repository so the stored address
follows.

**Everything is broken and I want to start over**, keeping nothing:

```bash
docker compose down -v && rm -rf ~/.config/flowlio && docker compose up -d && flowlio setup
```

This deletes every task, issue and memory entry. It is the nuclear option, listed because looking
for it is how people end up doing something worse.

---

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
> — the token is yours: `flowlio setup` files it on your machine and your agent carries it. In a hosted
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

Found a vulnerability? [SECURITY.md](SECURITY.md) says where to send it, and where not to.

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

---

## Without Docker

You need a Postgres 18 of your own and a Go toolchain. Nothing else — **not golang-migrate**: in
local mode the API carries its migrations inside the binary and applies them at start-up.

```bash
cp .env.example .env          # DATABASE_URL pointing at your Postgres 18
make run                      # applies the schema, then serves
```

The defaults differ from the Docker path in one way that matters: `ADDR` is `:8080` here, not
`:42058`. The first start writes `~/.config/flowlio/credentials.json` (`0600`) with the API URL
built from that address, so the CLI finds the right port on its own. A repository's `.mcp.json` no
longer records an address at all — it names a project and a repo, and the address travels with the
token in `~/.config/flowlio/repos/`, which `flowlio connect` rewrites. An agent set up against a
Docker instance no longer keeps calling `42058` forever.

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

Building the binaries instead of `make run`:

```bash
go build -o flowlio-api ./cmd/api && ./flowlio-api
go build -o flowlio ./cmd/flowlio      # the CLI, if you would rather not use a release archive
```

Backups here are yours to take against your own Postgres; the `pg_dump` line in
[Where your data lives](#where-your-data-lives) works the same without the `docker exec` prefix.

## Database

Postgres 18, in development as in production — no SQLite, no second SQL dialect to maintain.
Self-hosting uses the bundled `docker-compose.yml`.

On Neon, the API connects to the pooled endpoint (`-pooler`) with `default_query_exec_mode=exec` in
the DSN: PgBouncer in transaction mode is incompatible with pgx's prepared-statement cache. The
server refuses to start on a malformed DSN rather than failing later, under load.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) has the whole of it: how to get a development instance running,
what the guards check and why, the shape of a commit, and what gets a pull request refused. The
short version:

```bash
make check             # go vet + unit tests
make test-integration  # tests against the dev database
make lint              # golangci-lint + eight structural guards
```

Architecture (hexagonal, isolated modules, contracts separated from implementations) is described
in [CLAUDE.md](CLAUDE.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and `.claude/rules/` — in
French, like the rest of the internal documentation. It is enforced automatically: no cross-feature
imports, bounded file size, mandatory file summaries.

## Status

**v1.0.0.** The API, the CLI, the MCP server, the trust graph, the inbox, the per-repository memory,
the team debt queue and now the **cross-repo wake-up** are in and tested. The loop closes on its own:
a repo asks, its session dies, a sibling answers, and the waker relaunches the agent to read the
answer — proven end to end by an integration test and by `scripts/demo-wake.sh`. Setting up a
self-hosted instance is `brew install flowlio` and `flowlio`, or `docker compose up` then two
commands, and no secret is ever printed or pasted.

Not built yet: MCP over HTTP, a published Docker image, and the local web page the binary is meant to
serve on its own origin. On the wake-up, two polish items remain — a browser device-flow for
`flowlio login` (a pasted token works today) and an always-on `flowlio waker install` service.

Hosted accounts are **not part of this repository at all** — this engine runs `MODE=hosted` for an
operated deployment, and the accounts, billing and screens that go with it live in a separate
codebase. The one hosted seam here is the waker polling that operator's relay after `flowlio login`.

The full v1.0.0 release notes: [docs/releases/v1.0.0.md](docs/releases/v1.0.0.md). Scope and the
reasoning behind each decision: [docs/DESIGN-V1.md](docs/DESIGN-V1.md), and the wake design in
[docs/DESIGN-WAKE.md](docs/DESIGN-WAKE.md).

## License

[AGPL-3.0](LICENSE). Self-hosting is free and complete — no feature is held back.
