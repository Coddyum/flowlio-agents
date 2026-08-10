# Rule — the structure of a feature

Referenced by `CLAUDE.md`. Detail of the "mandatory patterns" piece.

## One uniform structure, without exception

**`handler/`**

- `handler.go` — struct, constructor, shared helpers (`writeJSON`, `writeError`,
  `claimsFromRequest`…).
- one file per endpoint: `create_user.go`, `delete_user.go`, and so on.

**`service/`**

- `service.go` — **contract only**: the service interface, the struct, the constructor, internal
  types, domain errors. **No implementation method.** A `func (s *service) xxx(...)` found in
  `service.go` is a violation.
- one file per business action: `claim_slug.go`, `update_theme.go`, and so on.
- when several actions form a coherent group and stay light, a single group file is acceptable
  (`sections.go`).

> **CRITICAL RULE — handler / service separation:**
> A file is either a handler file or a service file. Never both.
> When a feature has a cross-cutting domain (an external provider, say), the handlers go in
> `provider.go` and the service logic in `service_provider.go`. A `// --- service methods ---` in a
> handler file is an immediate violation.

**`store/`**

- `store.go` — the store interface, the struct, the constructor **only** — no implementation.
- methods grouped by entity in dedicated files: `profile.go`, `sections.go`, and so on.
- exception: when a store implementation grows (complex transactions, substantial mapping logic), it
  moves into its own file even if it belongs to an existing group.

## Handler naming conventions

- Struct: `Handler`, constructor: `New`.
- Fields: `auth authport.Service` + `svc ResourceService`.
- Never `AuthSvc`, `authService`, `Service` or `Auth` as field names.

## Outright violations

- An implementation in `store/store.go` (contract only: interface + struct + constructor).
- Any `func (s *service) xxx(...)` method in `service.go` — that file is a contract.
- Service code in a handler file, or handler code in a service file.
- Several business actions mixed into `service/service.go` (logic goes into separate files).
- A feature created without its `handler/`, `service/`, `store/` subdirectories.

## Documented exception

A feature may be **flat** (no `handler/service/store` subdirectories) only when it is a historical
exception, explicitly documented. Never replicate a flat feature for a new one.
