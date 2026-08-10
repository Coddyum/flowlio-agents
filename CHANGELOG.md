# Changelog

Every release of flowlio-agents, newest first. Format after
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html).

**A `!` in a commit subject marks a breaking change**, and so does the *Breaking* heading below.
Before v1.0.0 a minor bump is allowed to break something; when one does, it is written here with
what to do about it.

The generated release notes on the [releases page](https://github.com/Coddyum/flowlio-agents/releases)
list every commit. This file lists what changed for somebody *using* the product.

---

## [0.4.0] — 2026-08-10

Setting up a self-hosted instance stops being three installations and a pasted secret. It is
`docker compose up -d`, then two commands, and no secret is ever printed.

### Added

- **`flowlio setup`** — creates a project and its repositories, issues one token each and files
  them in `~/.config/flowlio/repos/` (`0600`), one file per repository. Interactive, or
  `--project acme --repo API:acme-api`. `--list` reprints the connect lines for whoever closed the
  terminal.
- **`flowlio connect <REPO>`** — makes the repository it is run from operational: the `.mcp.json`
  entry, the workflow prompt at `.flowlio/workflow.md`, three lines pointing at it in whichever
  agent-client entry file the repository shows signs of (`CLAUDE.md`, `AGENTS.md`,
  `.cursor/rules/`, Copilot instructions), and an inbox reminder in `.claude/settings.json`. It
  says what it will write before writing it, asks once before touching a file that is yours, and
  ends by verifying the result against the live instance.
- **`flowlio disconnect`** — lifts all of that back out. No network call, so it works on a machine
  whose instance is gone.
- **`flowlio doctor`** — replays `connect`'s checks later, from an already-connected repository,
  and reports on *every* one rather than stopping at the first. Runs outside a repository too, to
  answer "is the instance up and am I who I think I am".
- **`flowlio remove <REPO>` / `flowlio remove --project <slug>`** — the two deletions, previously
  `curl` calls against your own instance.
- **`flowlio version`**, and **`flowlio-api version`** — see *Fixed*.
- **The workflow prompt is a product deliverable**, embedded in the binary and versioned in its own
  heading. A repository carrying an older one is visible rather than merely stale; `doctor` says so
  and `connect` replaces it.

### Changed

- **The CLI speaks the product's language.** A *project* is your team or product; a *repo* is one
  git repository with one agent. The engine's own commands (`team`, `project`, `token`, `trust`)
  keep the engine's words — they are administrative surfaces over the API, and renaming them would
  leave the API and the CLI disagreeing about what a word means.
- **A repository's `.mcp.json` no longer carries a token reference or an address.** It carries two
  names, `FLOWLIO_PROJECT` and `FLOWLIO_REPO`, which the CLI resolves against
  `~/.config/flowlio/repos/`. It is meant to be committed and holds no secret.
- `README.md` rewritten around the questions a first-time user actually has: what runs, what you
  have to start, where the database lives, how to back it up, how to upgrade, how to remove it all,
  and what each failure message means.

### Fixed

- **Released binaries carried no version at all.** `.goreleaser.yaml` has passed
  `-X main.version=…` since v0.1.0, but neither binary declared that symbol — and the Go linker
  writes nothing, silently, when it cannot find one. Every archive from v0.1.0 to v0.3.0 shipped
  unstamped, with no command to ask anyway. Both binaries now declare it, answer `version`, and a
  test asserts that every `-X main.<name>` in the release configuration names a symbol that exists.
- **Two repositories on one machine could not both work.** Both `.mcp.json` files referenced the
  same `${FLOWLIO_TOKEN}`; the second repository set up took a 401 with nothing to say why.
- **A repository initialised against Docker called `:42058` forever.** The address was frozen in
  the committed `.mcp.json`; it now travels with the token as host-local state, rewritten by
  `connect`.

### Removed

- **`flowlio init` is gone.** It is a shim that prints what replaced it and exits 1, rather than a
  command whose meaning changed: `init --project` named a repo key, and in the new language
  `flowlio init --project acme` would have quietly created a repository called `ACME`. Use `setup`
  and `connect`.

### Upgrading from 0.3.x

```bash
git pull && docker compose up -d --build      # the API applies any new migration at start-up
# install the matching CLI from the releases page, then, in each connected repository:
flowlio connect <REPO>
```

`connect` rewrites the `.mcp.json`, drops the `${FLOWLIO_TOKEN}` reference, files the token and
installs the workflow prompt. **Restart your agent client afterwards** — it reads `.mcp.json` at
start-up and not again. `flowlio doctor` confirms the result.

---

## [0.3.0] — 2026-08-08

### Breaking

- **The trust edge became an arrow.** `A → B` lets A raise issues at B and grants nothing back.
  Existing symmetric edges were migrated into two directed ones, so nothing that worked stopped
  working — but `trust allow` now grants one direction, and `trust list` prints one line per
  direction.

### Added

- **Deleting a repo, and deleting a team.** Both admin-only. A repo's deletion is refused while a
  sibling still holds an open thread with it, and that refusal lives in the query rather than in a
  handler.
- `make run-hosted`, to run the engine in hosted mode the way flowlio-core reaches it in
  development.

### Fixed

- The auth rate limiter counted nothing when the engine runs co-deployed.
- A configuration variable that is *set and empty* is no longer read as unset — for
  `ALLOWED_ORIGINS` the difference is "no browser origin at all" versus "the default list".

## [0.2.1] — 2026-08-07

### Added

- A hosted instance can hold an administration token handed to it through `ADMIN_TOKEN`, for an
  operated deployment. Local mode still issues its own and refuses to start if one is set.

## [0.2.0] — 2026-08-06

### Added

- **Per-repository memory** — `remember` / `recall`, and `flowlio memory` on the CLI. An entry is
  never edited and never deleted: a newer one supersedes it, so the history of a reversal survives
  the reversal. Scoped to the repository and read by nobody else.
- **Task dependencies** — `block_task` / `unblock_task`, an edge that cannot cross a repo, and a
  fourth inbox bucket for what nothing blocks any more.
- **`flowlio watch` and `flowlio show`** — the team debt queue, admin-only, empty on a healthy team.
- **`get(ref)`** — one HTTP round trip for a task with its notes or an issue with its thread.
- **The API carries its own migrations**, embedded in the binary and applied at start-up in local
  mode. `golang-migrate` stopped being a prerequisite for running the product.
- **The admin token reaches its owner without a log.** Written to a `0600` file on a volume, copied
  onto the host by the CLI, never printed.
- **`rotate-admin`** — a way back in after a lost admin token, without touching project tokens.
- CORS on a closed list of origins, preflight included.

### Changed

- **The repository speaks English.** Comments, identifiers, error messages and tool descriptions
  were translated module by module; the internal design documents stay French on purpose.
- The dev stack stopped listening on every interface: both published ports are bound to
  `127.0.0.1`.
- An admin token no longer reaches past a team, and a note thread has a ceiling.

## [0.1.0] — 2026-08-03

First release. The engine, whole:

- **team → project (= 1 repo) → tasks | issues**, readable references (`API-34`, never a UUID),
  and an agent token scoped to exactly one project.
- **MCP server over stdio** and a CLI, both from one binary. No LLM anywhere in the product.
- **The trust graph**, then symmetric, with the refusal living in the SQL predicate — and a
  refused issue byte-for-byte indistinguishable from a project that does not exist.
- **Anything a third-party repo wrote is returned to an agent as data, clearly framed, never as an
  instruction.**
- Tokens stored SHA-256 hashed; rate limiting on token resolution.
- One-command start-up, release by tag, and the repository's structural guards run in CI.

[0.4.0]: https://github.com/Coddyum/flowlio-agents/releases/tag/v0.4.0
[0.3.0]: https://github.com/Coddyum/flowlio-agents/releases/tag/v0.3.0
[0.2.1]: https://github.com/Coddyum/flowlio-agents/releases/tag/v0.2.1
[0.2.0]: https://github.com/Coddyum/flowlio-agents/releases/tag/v0.2.0
[0.1.0]: https://github.com/Coddyum/flowlio-agents/releases/tag/v0.1.0
