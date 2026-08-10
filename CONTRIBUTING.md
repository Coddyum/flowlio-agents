# Contributing to flowlio-agents

Thanks for being here. This document is the whole of what you need: how to get the project running,
what the automated guards check and why they exist, and what will get a pull request refused before
anybody reads the code.

It is longer than most `CONTRIBUTING.md` files on purpose. This repository enforces a handful of
rules that are unusual — a summary block at the top of every Go file, a trust decision that may not
leave SQL — and finding those out from a red CI run is a worse first contribution than reading
about them here.

**Security vulnerabilities do not go in a pull request or an issue.** See [SECURITY.md](SECURITY.md).

---

## Before you write anything

### Open an issue first, for anything that is not a bug fix

A feature that arrives as a finished pull request is a feature somebody has to either accept or
throw away. Say what you want to do first; agreement takes a comment and saves you an evening.

Bug fixes, typos, documentation and test coverage need no ceremony — open the pull request.

### What is out of scope, permanently

These are not "not yet". They are decisions, with the reasoning written down in `docs/`:

| Not accepted | Why |
| --- | --- |
| **An LLM anywhere in the product** | Every behaviour here is deterministic and testable. A model in the loop is neither |
| **Accounts, billing, OAuth, a `users` table** | They live in a separate codebase, which is a *client* of this engine's administration API. Not a fork — see `docs/DECISION-hosted.md` |
| **An HTTP framework or an ORM** | `net/http` and sqlc. The dependency list is short and stays that way |
| **A new external dependency** | Adding one is a decision, not an implementation detail. Ask in an issue first |
| **SQLite, or a second SQL dialect** | One database, Postgres 18, dev and production alike |
| **A path added to a guard's tolerated list to make it pass** | That is how a rule becomes decorative |

A web UI is not on that list — it is *not built yet*, which is different. `docs/DESIGN-V1.md` has
the scope, in French.

---

## Getting it running

### What you need

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | 1.26.1 (from `go.mod`) | everything |
| Docker | any recent | the dev Postgres |
| golangci-lint | v2.11.4 | `make lint` |
| sqlc | 1.30 | only if you touch SQL |
| golang-migrate | v4.19 | only if you write a migration |

The pinned versions are the ones CI uses (`.github/workflows/ci.yml`). A different linter version
enables a different default rule set, and then your green run and CI's red one are both correct.

### Five commands

```bash
git clone https://github.com/Coddyum/flowlio-agents && cd flowlio-agents
cp .env.example .env          # DATABASE_URL already points at the compose Postgres
docker compose up -d postgres # just the database; you run the API yourself
make run                      # applies the embedded migrations, then serves on :8080
make check                    # go vet + unit tests — should be green before you change anything
```

`make help` lists every target.

The unit tests need **no infrastructure**: `go test ./...` runs on a clean checkout. The integration
tests need the dev database:

```bash
make test-integration
```

### Working on the CLI

```bash
go run ./cmd/flowlio help
go run ./cmd/flowlio setup --project demo --repo API:demo-api
```

Point it somewhere else with `FLOWLIO_API_URL` and `FLOWLIO_TOKEN`, which win over the credential
files. Handy when you want to leave your real instance alone.

---

## The rules the code follows

### Language: English

The repository is open source. **Everything the code carries is English** — comments, identifiers,
error messages, tool descriptions, `SOMMAIRE` descriptions, and commit messages.

The internal design documents (`CLAUDE.md`, `docs/ARCHITECTURE.md`, `docs/DESIGN-V1.md`,
`.claude/rules/`) are French and stay French; they are working documents for the maintainers, not
part of what the project publishes. A file you create is born in English even if its neighbours are
not: existing French is stock to clear as files are touched, never a local convention to match.

The one exception is the two literal markers of the summary block, below. They are matched verbatim
by a script and shared with sibling repositories.

### Layout

```
cmd/api/main.go          entry point of the server — the only place allowed to call log.Fatal
cmd/flowlio/             the CLI and the MCP server, one binary
internal/core/           the engine, the Module/CoreServices/FeatureRegistry interfaces
internal/feature/<name>/ a feature: handler/ + service/ + store/
internal/database/       sqlc output — generated, never edited by hand
internal/pkg/            config, credentials, client, crypto, cache, termtext
sql/                     migrations / queries (sqlc) / schema snapshot
```

Adding a feature means creating `internal/feature/<name>/` and adding one line to `buildModules()`
in `cmd/api/main.go`. Nothing else.

### Data flow — the rule that does not bend

```
handler  →  service  →  store  →  DB
```

| Layer | May reach | Must not touch |
| --- | --- | --- |
| **handler** | its service | a store, `*database.Queries`, `*sql.DB` |
| **service** | a store interface | `*database.Queries`, `*sql.DB` |
| **store** | `*database.Queries` | business logic, HTTP |

`service.go` and `store.go` are **contract files** — interface, struct, constructor. No
implementation goes in them. A file is either handler or service, never both.

### The `// SOMMAIRE` header

**Every `.go` file with two or more top-level declarations carries a summary block** right after
`package xxx`: one table row per `func` or `type`, a one-sentence description, and the line number.
The point is jumping straight to the right passage without reading the file first.

```go
package focus

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                      | Ligne |
// |-----------------|-------------------------------------------------------------|-------|
// | Module          | Holds the focus module's own dependencies                    | 11    |
// | NewModule       | Builds the module from its queries and JWT secret            | 16    |
//
// Fin du sommaire.
// =====================================================================
```

- The two markers stay in French, verbatim. `scripts/check-sommaire.sh` matches them literally, and
  the three Flowlio repositories share them.
- The descriptions are English, and are written from reading the code — not from restating the name
  of the function.
- One row per `func` or `type`. Not `var`, not `const`: the guard counts lines starting with
  `func ` or `type `, so a row for a `var` makes the count disagree and fails.
- Excluded: `internal/database/*`, anything headed `// Code generated … DO NOT EDIT`, and `_test.go`.
- Add, remove or rename a declaration and you update the table in the same commit. **Line numbers
  are bookkeeping** — run `make sommaire` and they are fixed for you. It never adds or removes a
  row: writing the description of a new declaration is a judgement call and stays with the author.

### Errors

- Wrap with context: `fmt.Errorf("user store: get by id %s: %w", id, err)`.
- `errors.Is` / `errors.As`, never a string comparison.
- No `log.Fatal` outside `cmd/api/main.go`. No `panic` in business logic.
- A log line says **what** failed, **where**, and **why**, without opening the code.

### Style

- No `func init()`. No mutable package-level `var` for shared state — it travels through
  `ModuleConfig` or `CoreServices`. (`cmd/*/version.go` is the documented exception: the linker's
  `-X` can only write into a `var`.)
- Small interfaces, table-driven tests, no `any` without a stated reason.
- A `.go` file over 300 lines is a signal to split, and a guard says so.

### Tests

**A guarantee is proven by mutation.** Break the thing on purpose and watch the test go red; a test
that stays green when the behaviour it covers is deleted guards nothing. Several tests in this
repository exist because a mutation survived — the comments say which one.

Be wary of assertions written in the negative (`does not contain`, `is not called`): they also pass
on empty output. Say what the output **is**.

### Database

- Migrations go in `sql/migrations/`, applied with `make up-dev` against the local database.
- **`make up-prod` is for humans only.** No exception.
- A destructive migration (`DROP`, a lossy `ALTER`, `TRUNCATE`) needs explicit agreement *before* it
  runs, in dev too.
- SQL queries live in `sql/queries/` only, never inside a `.go` file.
- `internal/database/` is generated by `make sqlc` — regenerate it, never hand-edit it.
- Update the schema snapshot with `make schema` after a migration.

---

## The guards

`make lint` runs eight scripts, `make check` runs vet and the tests, and a local `PostToolUse` hook
runs a subset after every `.go` edit. CI runs all of it. Each script has its reasoning at the top of
the file — the *why* is the part worth reading.

| Guard | Refuses |
| --- | --- |
| `check-cross-feature-imports.sh` | a feature importing a sibling feature directly; go through `FeatureRegistry` / `CoreServices` |
| `check-file-size.sh` | a `.go` file over 300 lines (generated code and `_test.go` excluded) |
| `check-sommaire.sh` | a missing or desynchronised `// SOMMAIRE` block |
| `check-trust-in-sql-only.sh` | the trust decision moving out of the `EXISTS` in `CreateIssue`'s `WHERE` — measured, moving it opens a write channel *and* a denial of service |
| `check-overview-scope.sh` | the one module that reads by `team_id` alone drifting out of its exception |
| `check-admin-team-scope.sh` | an admin route that does not bound a team. `AdminOnly` proves a token's scope, never a request's |
| `check-seal-source.sh` | the framing seal being drawn from `math/rand`. A predictable seal makes the whole untrusted-content contract decorative |
| `check-authtest-not-in-production.sh` | the auth test harness reaching a production file, which would be an auth path that never consults the database |

**Never add a path to a guard's tolerated list to make it pass.** If a guard is wrong, say so in the
pull request and argue it — that conversation is worth having, and silently widening the guard is
not.

---

## Commits and pull requests

### Commit messages

[Conventional Commits](https://www.conventionalcommits.org/), in English, with a scope:

```
feat(cli): a repository says what it is connected to, in one command
fix(auth): a rate limiter that counts nothing when the engine is co-deployed
docs(readme): the daily-use section answers the question people actually ask
```

Types in use: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `ci`, `i18n`. A `!` after the
scope marks a breaking change (`feat(trust)!: …`).

The subject describes **what became true**, not what you did to the files. `fix(config): a variable
that is set and empty is not a variable that is unset` says more than `fix config parsing`, and
costs the same.

The release notes are generated from these, with `docs:`, `test:`, `chore:` and `ci:` filtered out —
so a `feat:` or a `fix:` subject is user-facing text. Write it that way.

The body is where the *why* goes: what you measured, what you tried that did not work, what a
reviewer would otherwise have to reconstruct.

### Before you open it

```bash
make check              # vet + unit tests
make lint               # golangci-lint + the eight guards
make test-integration   # if you touched SQL, a store, or a handler
```

All three green. CI runs the same thing and will tell you, but it takes longer than you do.

### The pull request itself

- **One change per pull request.** A refactor bundled with a fix costs the reviewer the ability to
  accept one and not the other.
- Target `main`. Every release tag has to descend from it — the release workflow refuses a tag that
  does not.
- Fill in the template. "What this changes" and "how you know it works" are the two questions a
  reviewer will otherwise have to ask.
- If you added behaviour, add the test that goes red without it. Say in the description which
  mutation you checked it against.
- CI has to be green, and a maintainer has to approve. Both are enforced by branch protection, not
  by good manners.

### What happens next

Maintainer review — [@Coddyum](https://github.com/Coddyum). Expect questions about *why* more often
than about *how*; this codebase writes its reasoning down, and a change that arrives without it will
be asked for it.

---

## Releasing

Maintainers only, and one command's worth of ceremony. Pushing a tag is what publishes — which is
why `v*` tags are protected; [docs/GITHUB-SETTINGS.md](docs/GITHUB-SETTINGS.md) has that and every
other repository setting that is not a default.

1. `CHANGELOG.md` gets the release's section, dated, written for somebody using the product.
2. The `## Status` line in `README.md` names the new version.
3. `make check && make lint && make test-integration`, on `main`, all green.
4. `git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z`.
5. The release workflow replays the guards, refuses the tag if it does not descend from `main`,
   builds both binaries for linux and darwin on amd64 and arm64, and creates a **draft** release —
   a tag pushed by mistake publishes nothing to anybody.
6. Read the generated notes, then publish the draft by hand.

`flowlio version` on the downloaded archive should print the tag. If it prints `dev`, the stamping
broke — and there is a test that fails before you get that far.

---

## License

By contributing you agree that your contribution is licensed under the
[AGPL-3.0](LICENSE), like the rest of the project.
