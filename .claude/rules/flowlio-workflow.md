# Rule — tracking this project in Flowlio (MCP `mcp__flowlio__*`)

Referenced by `CLAUDE.md`. Flowlio is the tracker that carries the state of **this** project between
sessions. It replaces markdown tracking files: no `PROGRESS.md`, `TODO.md` or `NEXT-STEPS.md` may
exist in this repository.

Three levels of memory, not to be mixed up:

| Level       | Tool                 | Lifetime                                            |
| ----------- | -------------------- | --------------------------------------------------- |
| Session     | `TodoWrite`          | dies with the conversation                           |
| Project     | **Flowlio**          | survives sessions — the real state of the product    |
| Design      | the repo's `docs/`   | decisions and architecture, versioned with the code  |

An architecture decision goes into `docs/`, not into a task. A task says **what to do**, `docs/` says
**why it is the way it is**.

---

## The board

Team **Flowlio** (`FLOWL`) → project **FLOWLIO_IA** (`FLWL`).

Resolve the ids by name every session (`list_teams` → `list_projects` → `list_columns`).
**Never hard-code an id**: the board can be reorganised between two sessions.

| Column               | Contents                                                             |
| -------------------- | -------------------------------------------------------------------- |
| `Ready`              | Ready to start, scope clear. **This is where you pick from.**         |
| `Unnamed column`     | Backlog — milestones not opened yet (to be renamed "Backlog" in the UI) |
| `In progress`        | Under way in the current session. One task at a time.                 |
| `Blocked / decision` | Waiting on a call from Maxence, or on an undelivered dependency.      |
| `Done`               | Delivered, tested, committed.                                         |

---

## Session protocol — not negotiable

**At start-up, before anything else:**

1. `list_project_tasks` on FLOWLIO_IA — it is the source of truth for what is left to do.
2. Read the `In progress` column: a task lingering there signals an interrupted session. Pick that
   one back up before opening a new one.
3. Read `Blocked / decision`: if Maxence has ruled since, unblock it.
4. `get_task` on the chosen task to get its full scope, then `move_task` to `In progress`.

**During:** `TodoWrite` for the session's fine-grained breakdown. Do not duplicate it into Flowlio.

**At the end of a session, every time:**

- `update_task`: complete the description with what is done, what is left, and the decisions taken.
  This is what the next session will read — write it for somebody with no context at all.
- `move_task` to `Done` when delivered and committed, `Blocked / decision` when it waits on a call,
  otherwise leave it in `In progress` with its state up to date.
- Create a task for any work spotted along the way and not done.

> A session that ends without updating the board costs the next one its context. It is the only
> genuinely expensive failure of this arrangement.

---

## Archiving

`archive_task` as soon as a task is **really** finished: delivered, tested, committed, and its
milestone closed. Not "tidied away" — finished.

Keep the last delivered milestone in `Done`: it is the next session's landmark. Archive the ones
before it. An archive stays readable (`list_project_archived_tasks`, `get_archived_task`) and comes
back with `unarchive_task` in case of a regression.

Never look in the archives for what to do now: that is `list_project_tasks`'s job.

---

## Writing a task

Full markdown is supported, and the board is meant to be scanned in seconds:

- **A table** as soon as there is a correspondence to list (piece/state, option/cost)
- **A code block** for a signature, a command, an API surface — never described in prose
- **A blockquote** (`>`) to isolate what matters: a security constraint, a blocking dependency
- `##` to split "Scope" / "Rules" / "Done when"

Every development task carries a **Done when** section, expressed as verifiable criteria (`make
check` green, an integration test covering X), not as intentions.

If the description goes beyond what reads in 30 seconds, the task is too big: split it, or point at
`docs/DESIGN-V1.md`.

---

## Secrets — specific to this project

> This project **manufactures tokens**. An `flw_...` pasted into a task description is a secret
> published on a third-party board, and Flowlio has no deletion tool: only the archive, which keeps
> the content.

Never a token, a DSN with a password, or the contents of `~/.config/flowlio/credentials.json` in a
task. To illustrate one, write `flw_<prefix>_<secret>`.

---

## Label vocabulary

The server refuses unknown labels. Observed:

| Field      | Valid       | Refused                |
| ---------- | ----------- | ---------------------- |
| `priority` | `urgent`    | `high`                 |
| `status`   | —           | `in-progress`          |

Defaults: `no-priority` / `no-status`. Do not guess a label — the column already carries the state,
and priority only marks what goes ahead of the rest. Full list to be confirmed with Maxence if the
need comes up.

---

## What Claude does not do

- Start coding without having read the board — the previous session left its state there
- Guess a team, project, column or task id without the matching `list_*`
- Create a task for a trivial fix done on the spot (`TodoWrite` is enough)
- Recreate in markdown a tracking the board already carries
- Put a secret, a token or a DSN in a description
- Archive an unfinished task, or edit an archived one without `unarchive_task` first
- Leave a task in `In progress` with a stale description at the end of a session
