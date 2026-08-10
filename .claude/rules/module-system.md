# Rule — the module system

Referenced by `CLAUDE.md`. Detail of the "module system" and "imposed architecture" pieces.

## Interfaces and wiring

- Interfaces in `internal/core/module/module.go`. Wiring in `cmd/api/main.go`. No `func init()`.
- `CoreServices` exposes **shared services** only (e.g. `Auth()`, `Billing()`), never a
  feature-specific service.
- `ModuleConfig` gathers all the shared infrastructure (DB, RawDB, Config, Ctx, Cache…) — one single
  parameter per `NewModule()`.

## Rules between modules

- Modules **never import** other modules directly. Checked automatically (blocking hook on edit +
  `make lint`).
- Every cross-feature dependency goes through `FeatureRegistry.Get("key")` or `CoreServices`.
- Adding an inter-module interface to `module.go` = a critical file, validate it with the human.
- If `FeatureRegistry` is received and never used, drop it.
- Map of the existing interfaces: `docs/ARCHITECTURE.md`.

## Other structural rules

- **Middleware**: bound once in `module.go`, never inside a handler.
- **Config**: `NewModule(cfg module.ModuleConfig)` — never direct params (db, secret, timeout…).
- **Store**: the service receives a local interface, never `*database.Queries` directly.
- **Transactions**: expose a `Transactor` on the store — `*sql.DB` never leaks into a service.
- **Singletons**: no mutable global `var` — all state travels through `CoreServices` or
  `ModuleConfig`.

## File size

- A `.go` file over 300 lines (excluding generated `internal/database` and `_test.go`) is to be
  split.
