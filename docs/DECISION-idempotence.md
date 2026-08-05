# Decision — idempotence of `create_task` and `create_issue`

> Note produced on 2026-08-02 by a design fan-out (four independent angles — client key, server
> fingerprint, product, security — each critiqued by an adversarial agent), then verified by
> execution against the dev database.
>
> **Status: DECISION SETTLED on 2026-08-02 by Maxence — no deduplication will be built.**
> Reason given: "playing on a few milliseconds is ridiculous". This document exists so that the
> next session does not have to redo the analysis, and so that the decision can be contested on
> facts rather than re-improvised.
>
> What would reopen the subject: an MCP client that **mechanically replays** a tool call whose
> response was lost. None does today; the day one does, the variant to pick is the content
> fingerprint over a short window, and nothing else.

---

## What the task claimed

> An agent calls `create_issue`, the response is lost (timeout, session killed, context compacted
> at the wrong moment). The agent **replays** the call: a second identical issue at the
> recipient's, and a number burnt in a sequence whose density is an invariant of the product.

Two of the three claims in that paragraph are false, and the third is narrower than announced.

## Fact 1 — an interrupted creation leaves nothing behind

`internal/pkg/client/client.go` builds every request with `http.NewRequestWithContext` and a
15 s `requestTimeout`. The handlers pass `r.Context()` to the service; `WithTx` opens through
`s.db.BeginTx(ctx, nil)`. Timeout, session killed, agent interrupted: the context is cancelled,
and the transaction is cancelled with it. **No row, no number consumed.**

Proven by execution, on both paths:

| Test                                     | File                                                 |
| ---------------------------------------- | ---------------------------------------------------- |
| `TestCancelledRequestCreatesNothing`     | `internal/feature/task/store/store_integration_test.go`  |
| `TestCancelledRequestOpensNothing`       | `internal/feature/issue/store/store_integration_test.go` |

Both cancel the context **after** the number is reserved — the worst possible instant — and check
that no row exists and that the replay does get number 1.

> Non-vacuity checked by mutation: detaching the transaction from the context **is not enough** to
> make the test fail, because the statement itself carries the cancelled context. BOTH mechanisms
> have to be removed — `BeginTx(ctx)` and the propagation of `fn`'s error — for the test to fall
> over. The property is defended in depth, this is not a complacent test.

The only window where a replay really duplicates is the interval between the successful `COMMIT`
and the bytes reaching the client: of the order of a millisecond.

## Fact 2 — a duplicate burns no number

`ClaimNumber` and the insert are in the same transaction. Two creations that succeed produce
`CORE-34` **and** `CORE-35`: two rows, no gap. The density of the sequence is only threatened by a
**failure**, and a failure rolls back (`TestFailedCreateDoesNotBurnNumber`).

The residual damage of a duplicate therefore reduces to: one object too many, and a second
`issue.opened` event that relights `is_new` at the peer's — one turn of context for the sibling
repo.

## Fact 3 — the replay is not mechanical, so no device catches it

`docs/DESIGN-M3.md` already says it: "will be replayed by **the agent**". The replayer is the
model, in a new session, from a recomposed context. There is no retry loop in the repository:
`Client.Do` makes a single `c.http.Do(req)` and yields the error as such.

Consequence, device by device:

| Device                                      | Why it misses the real replay                                                                  |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Idempotency key minted by the client        | fresh on every tool call, never seen again: zero replays detected, ever                          |
| Content fingerprint                         | diverges as soon as the model rewords a `body` for which the tool demands "the full context"     |
| Business identity `(recipient, title)`       | catches things other than replays, and breaks the follow-up (`closed` is terminal, reopening = re-asking) |

## Fact 4 — no device can TELL the agent

`Client.Do` yields nothing but an `error`: `200` and `201` are indistinguishable to the MCP layer.
Exposing a `replayed` means reopening the signature of the HTTP client shared by CLI and MCP, or
adding a field to a DTO that comes back out in `list_tasks`, `get` and `update_task` — that is to
say paying context budget on **every turn of every session**, indefinitely.

Without that signal, a deduplication yields an object whose state may have moved: an issue already
`answered`, a task already archived, in answer to what the agent believes to be a creation. The
next call then 404s — `AnswerIssue` carries `AND i.state <> 'closed'`, `UpdateTask`, `ArchiveTask`
and `CreateTaskNote` carry `AND archived_at IS NULL`.

## The asymmetry that settles it

| | Cost | Visible? | Repairable? |
| --- | --- | --- | --- |
| **False negative** (duplicate not detected) | one object too many, one turn of context at the peer's | yes | yes — `update_task(archive=true)`, `answer_issue(close=true)` |
| **False positive** (creation wrongly suppressed) | a question never asked, on the most expensive path of the product | **no** | **no** |

We keep the loud, repairable flaw rather than the silent, permanent one.

## What was shipped instead

1. The two cancellation tests above, which **bound** the problem instead of assuming it.
2. Decision #23 of `docs/DESIGN-M3.md`, prescribed and never applied on the `task` side: a
   violation of `tasks_number_unique_per_project` now surfaces `ErrCorrupted` → 500, and not
   `ErrConflict` → 409. A corrupted counter answered "conflict" to an agent that had done nothing
   wrong and would retry indefinitely. Proven by `TestDuplicateNumberIsCorruptionNotConflict`,
   which falls over if the discrimination is removed.

## What stays open, and is a human call

- **If an MCP client did retry mechanically** a tool call whose response was lost, a content
  fingerprint would catch it (the arguments would be identical byte for byte). No known client
  does so today; the day one does, this note is to be reopened.
- **`CHECK` violations (`23514`)**: they all duplicate an application-level validation, so
  reaching one means the validation diverged from the schema — a server failure, not a caller
  conflict. Mapping them to 500 is consistent with the above, but it is a behaviour change wider
  than decision #23: out of scope here, to be settled.
