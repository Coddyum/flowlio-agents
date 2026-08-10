# Rule — code conventions

Referenced by `CLAUDE.md`. Detail of the "code conventions" piece.

## Language — English

> The repository is open source. A history and comments in French exclude contributors and read as
> somebody's internal project.

- **Commit messages: English.** Without exception, since 2026-08-05.
- **Every text the code carries: English** — comments, `// SOMMAIRE` descriptions, identifier names,
  error messages, CLI help, guard output, `make help` descriptions, release notes.
- **A new file is born in English**, even when its neighbours are French. Never "harmonise" a new
  file back into French for the sake of local consistency.
- **`cmd/` and `internal/` have been English since 2026-08-05.** No French survives there, summary
  markers excepted. A regression is treated as an ordinary style mistake, not as debt.
- **`docs/`, `sql/`, the configuration files and the scripts followed on 2026-08-10.** The rule that
  applied until then — translate a file only when opening it for another reason — was withdrawn by
  Maxence that day, and the remaining stock was cleared in one pass. There is no French left to
  clear; there is only French not to write.
- Exceptions, and only these: the **literal markers** of the summary block
  (`SOMMAIRE (lire en premier…)`, `Fin du sommaire.`) and the `| Élément |` header row, compared
  verbatim by `scripts/check-sommaire.sh` and `scripts/sync-sommaire-lines.sh` and shared with
  `flowlio-core` and `Flowlio`; the **Flowlio task descriptions** and the conversations with
  Maxence, which are not code.

> **A translated heading without its citations is worse than a French heading**: the citation points
> at a section that no longer exists. Before renaming a `##` in `docs/`, look for who cites it —
> `grep -rn '§ <heading>' --include='*.go' --include='*.sql' --include='*.md'` — and change
> everything in one gesture. Citations from `sql/queries/` are copied by sqlc into
> `internal/database/`: a `make sqlc` is part of that gesture.

Two headings are cited from code today, and they are the ones to be careful with:

| Heading | Cited by |
| --- | --- |
| `docs/DESIGN-TUI.md` § *Security guarantees* | `cmd/flowlio/mcp_overview_test.go`, `internal/feature/overview/{module,handler/handler,store/store_integration}_test.go` |
| `docs/DESIGN-TRUST.md` § *The indistinguishable refusal* | `cmd/flowlio/mcp_refusal_test.go`, `internal/feature/issue/module_integration_test.go`, `internal/feature/issue/provider.go` |

## Naming

> If the name of a variable, function or file needs a comment to be understood, the name is wrong.
> Rename first.

- Explicit names (`userSessionStore` over `uss`, `createUserHandler` over `cuh`).
- Files named after one single, clear responsibility.

## Error handling

- A log has to say **what** failed, **where**, and **why**, without going back to the code.
- Always wrap with context: `fmt.Errorf("user store: get by id %s: %w", id, err)`.
- **`log.Fatal` is forbidden outside `main.go`** and start-up initialisation.
- No `panic` in business logic.

## Principles

- **Performance**: no needless work, no avoidable allocation.
- **DRY**: a pattern repeated more than twice gets extracted.
- **SRP**: one responsibility per file, function and type.
- No over-engineering: if the simple solution is enough, it is the right one.

## General style

- Idiomatic Go conventions (`errors.Is`/`errors.As`, small focused interfaces, table-driven tests).
- No `interface{}` / `any` without an absolute, stated need.
- No ORM.
- No `func init()`.
