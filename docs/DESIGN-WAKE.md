# DESIGN — Waking a dead agent, and the single command that ships it

Written 2026-08-11. This is the plan for the cross-repo wake-up, its transport, the daemon that
runs it, and the way both are delivered to a user.

> **Relationship to what already exists — read this first.**
>
> - It **expands FLWL-8** (“M6 — Wake-up of a dead session”). FLWL-8’s cost model and transport are
>   kept verbatim (see §3); what this doc adds is the delivery shape (`brew install`, one `flowlio`
>   command) and **agent-agnosticism**, which FLWL-8 did not have — FLWL-8 assumed Claude
>   throughout.
> - It fills the hole **DECISION-setup.md** left open on purpose: *“A published Docker image and an
>   installer. They will get their own decision.”* This is that decision.
> - It keeps **D55** (the probe cost model) unchanged. D55 is validated and not reopened here.
> - It respects **D24** (this repo holds no accounts, no billing, never runs a customer’s agent),
>   **D27** (Postgres only, no SQLite), **D38** (compose ports bound to loopback), and the
>   per-repository config files of DECISION-setup.md.
>
> **One change to an already-validated decision is recorded here and must be written to the memory
> register** (`remember`, then copied to `docs/decisions.md`): FLWL-8 was Claude-only; this plan
> makes the *resume-the-dead-session* behaviour Claude-specific and adds a generic *fresh-launch*
> path so codex / opencode / any agent are first-class. The FLWL-8 card should be updated to point
> here.

---

## 1. The problem, stated plainly

The whole point of flowlio-agents is that sibling repos ask each other questions (issues) instead
of a human carrying the message. Today they still need the human: an agent only calls `check_inbox`
when a human starts a session. When repo B answers repo A’s question, the session in A that asked is
already dead, and nobody learns the answer until a human re-launches it. The loop does not close by
itself — the product moved the bus-driving, it did not remove it.

An agent is an **ephemeral process**, not a daemon. It cannot listen. So something persistent, next
to where the agent’s code and credentials already live, must catch the signal and run the agent.
That something is the **waker**.

---

## 2. The shape in one picture

```
THE ENGINE  (cmd/api — long-lived HTTP server)
   probe endpoint       "is there anything past my cursor?"  → zero SQL in steady state
   POST /wake (local)   engine → waker on 127.0.0.1 when an event drops (self-host only)

THE WAKER  (part of the `flowlio` program — runs on the user's machine, NOT in a container)
   learns "event for repo X" → looks up repo X's local path → launches the user's agent there
   agent-agnostic: claude / codex / opencode / custom command

THE AGENT  (claude/codex/… — already installed and logged in on the user's machine)
   runs check_inbox via its .mcp.json, answers, exits
```

One product change (the probe + wake surface, most of it already half-built). One companion (the
waker, folded into the `flowlio` program the user already has). Nothing sensitive leaves the user’s
machine.

---

## 3. What is KEPT from D55 / FLWL-8 — do not reinvent this

This is settled and was hard-won (Maxence measured a naive ticker burning astronomical CU/hour on
another project). A new session must build on it, not around it.

| Piece | Rule |
| --- | --- |
| **Two questions, split** | *“Is there anything?”* is an integer-vs-integer compare held in memory (`max(events.id)` of the team vs the token cursor) — **zero SQL** in steady state. *“What is it?”* is the 6-query `check_inbox`, and only fires when the first says yes. Cost follows **real events**, never time × agents. |
| **Server dictates cadence** | The probe reply carries `next_probe_after`; a client that ignores it gets a `429`. A misconfigured daemon cannot cost the day. |
| **Escalation ladder** | 5 empty probes per rung, promote on empty, reset to rung 0 on any event: 1 min → 2 → 5 → 15 → 1 h → **6 h cap**. Used by the daemon **only when it cannot be pushed to** (hosted, behind NAT). |
| **`POST /wake` local** | Self-host only: engine and waker on the same machine, engine hits `127.0.0.1` the instant an event drops — zero polling, zero latency. Carries a **secret handed at registration** so no other local process can trigger a run. Not SSE: an ordinary HTTP request the other way, closed immediately, no per-connection state. |
| **Piggyback** | Every MCP tool response carries the inbox counter in its envelope. An **active** agent learns it was answered with no extra request. Polling only ever serves the **inactive** agent — exactly the one the ladder has already parked at 6 h. |
| **Session lease** | A session registration carries a 15-minute lease. An agent that crashes stops costing on its own; an unrefreshed registration expires. |

> The cost property is **tested, not asserted**: an integration test counts SQL queries and stays at
> **zero across 100 empty probes**. Removing the cache turns it red.

---

## 4. What is NEW in this plan

### 4.1 Delivery — one install, one command

```
brew install flowlio            # or a curl installer / a downloaded release binary
```

One program. The first run asks which mode, and from then on a single command runs everything:

| Mode | Command | What runs |
| --- | --- | --- |
| **self-host** | `flowlio` | DB (Postgres 18, in a container `flowlio` manages) + engine + waker, one process tree, one terminal |
| **hosted** | `flowlio login` once, then `flowlio` | the waker only — the engine runs on our infra |

No two terminals, no “start this, then start that”. The waker is **not a separate binary**; it is a
mode of the `flowlio` program the user already has for the CLI and MCP.

### 4.2 Agent-agnostic — the one real departure from FLWL-8

FLWL-8’s resume mechanism (`claude -r <session-id>`, `~/.claude/projects/…`, the `SessionStart`
hook) is **Claude-specific**. codex and opencode have no verified equivalent. So the waker supports
two launch styles, chosen per configured agent:

| Style | When | What the waker runs |
| --- | --- | --- |
| **resume** | agent supports it AND a live session id is known (Claude) | `claude -r <session-id> -p "<prompt>"` — re-enters the exact session that asked, with its context |
| **fresh** | any other agent, or Claude with no known session | `<command template with {prompt}>` in the repo’s directory — the agent starts cold and rebuilds context from `check_inbox` |

Configuration, set at install and changeable anytime:

```
flowlio agent set claude            # preset: resume-if-known else fresh
flowlio agent set codex             # preset: fresh, `codex exec {prompt}`
flowlio agent set opencode          # preset: fresh, `opencode run {prompt}`
flowlio agent set-custom "mytool --headless {prompt}"   # unknown agents, {prompt} injected
```

Presets carry the exact headless invocation so the user types nothing technical. `{prompt}` is where
the waker injects *“You have inbox items — run check_inbox and act on them.”*

The model: the waker launches the command, **waits for it to finish and exit** (non-interactive), then
returns to waiting. No lingering session.

---

## 5. Self-host mode, in detail

`flowlio` (single command, self-host) does, in order:

```
flowlio
  ├─ DB container down?     → bring up a Postgres 18 container it manages
  │                           (data in a volume; stop/start loses nothing; port on loopback, D38)
  ├─ migrations behind?     → apply them (local only, D32)
  ├─ start the engine       (native process on the machine)
  └─ start the waker        (native; registers a local POST /wake secret with the engine)
  → one terminal, everything running
```

- **Transport: `POST /wake` local.** Engine and waker share the machine; the engine pushes the
  instant an event drops. No polling, no ladder needed here.
- **DB in a container is fine** — it only ever needs its own volume, it reaches nothing else on the
  machine. (The *waker* is what cannot be containerised: it must reach the user’s repos, agent
  binary and credentials, which a container walls off. Different needs, different answers.)
- **Postgres option** for power users: `flowlio` accepts an existing DB URL instead of managing a
  container. The container path is the default; the URL path is the escape hatch.
- **Always-on** (survive a closed terminal / reboot): `flowlio waker install` drops a launchd
  (macOS) / systemd (Linux) service. Polish, not required to start.

---

## 6. Hosted mode, in detail

```
flowlio login          # device-flow OAuth or paste an account token — links this waker to the account
flowlio                # runs the waker only, connected to prod
```

- The **engine runs on our infra** (co-deployed with flowlio-core, D25). The waker still runs on the
  user’s machine, because the agent’s code and credentials are there and **never** on ours.
- **Transport: the escalation ladder**, not `POST /wake` — a laptop behind a NAT is not reachable
  from prod. Piggyback still frees active agents from polling.
- **Identity, nothing more:** on start the waker phones home — *“runner for account X, alive”* — and
  the engine validates it. We hold **no code, no agent credentials, no repo** — only the fact that a
  runner is connected. This keeps hosted inside D24: we host coordination, never execution.
- **Free.** The waker is the same program the self-host user runs; hosted simply omits the engine.
  A paying customer is not asked to rent a box — they run one small local process, the same gesture
  as any background agent.

> **Explicitly out of scope, and it stays out:** we never run the customer’s agent on our infra
> (“managed execution”). That would drag their code, their credentials and the liability onto us —
> a different product, against D24. If it is ever built it is a warm micro-VM pool (Fly / Modal /
> Vercel Sandbox booting from a pre-baked snapshot), **never** a fresh CI runner per event.

---

## 7. Which directory does the waker run the agent in

The waker may cover several repos on one machine. An event for repo X must launch the agent in **X’s
directory**, nowhere else. Two sources, matching the two launch styles:

- **Claude (resume):** the `SessionStart` hook already receives `session_id` + `transcript_path` when
  a human starts a session. The waker registers `(session_id, repo, path)` from it. No manual step.
- **Any agent (fresh):** `flowlio connect` is run *from inside* the repo directory, so its working
  directory **is** the path — captured with zero guessing and stored host-local, alongside the
  per-repository token/address files DECISION-setup.md already defines
  (`$XDG_CONFIG_HOME/flowlio/repos/<project>/<REPO>.json`). No local path ever goes into a database,
  self-host or hosted — a filesystem path is machine-local, not product data.

---

## 8. The wake → run sequence, end to end

```
issue lands for repo X
  → engine writes the event (already the case since M3)
  → engine, in memory: team head id > this token's cursor → there is something for X
  → SELF-HOST: engine POSTs /wake to the local waker (with the registration secret)
     HOSTED:    the waker's next escalation probe returns "something for X"
  → waker looks up X → local path (+ session id if Claude)
  → waker launches the configured agent:
       resume:  claude -r <session-id> -p "<prompt>"
       fresh:   <command template> in X's directory
  → agent runs check_inbox via its .mcp.json, answers the issue, exits
  → waker returns to waiting
```

`.mcp.json` serves the **agent** (how it talks to the engine). The host-local config serves the
**waker** (where to launch, how). Two files, two roles, no path in the DB.

---

## 9. Guardrails — this is the point, not a footnote

| Guardrail | Why |
| --- | --- |
| **Relaunch cap** | A pair of repos can answer each other in a loop. `-p` is non-interactive: a re-launched agent that re-blocks **files a new issue** rather than waiting. Without a cap, two repos burn a whole session in mutual relaunches. |
| **Wake secret** | The local `POST /wake` carries a secret handed at registration. Without it, any process on the machine could relaunch agents — and every relaunch spends quota. |
| **Server-dictated cadence** | `next_probe_after` + `429`. A daemon misconfigured to 1 s cannot cost the day. |
| **Lease** | An unrefreshed session registration expires; a crashed agent stops costing on its own. |
| **Zero-SQL probe, tested** | Integration test counts queries, stays at zero across 100 empty probes; removing the cache turns it red. |

---

## 10. Scope — v1 is one machine

**In:** a single machine per user; self-host and hosted; agent-agnostic launch; the full cost model.

**Deferred, noted so it is not lost:**

- **Multi-machine “who answers.”** Same user on laptop + desktop = two wakers for repo X = a risk of
  two agents answering the same issue. Needs a “single active waker per repo” lock. Blocks nothing
  today; parked.
- **Managed execution** (we run the agent) — §6, against D24 unless it ever becomes a deliberate
  paid product.
- **An embedded Postgres binary** (no-Docker self-host). The container path assumes the self-host
  user has Docker, a reasonable bet for a technical audience.

---

## 11. Build order

Each phase ends on a verifiable criterion. `make check` green is implied throughout.

1. **The probe surface** — the in-memory “is there anything?” endpoint, zero SQL in steady state.
   *Done when:* the query-counting test stays at zero across 100 empty probes; a real event makes it
   return non-empty.
2. **`POST /wake` local + registration + secret + lease.**
   *Done when:* a registered waker is hit on `127.0.0.1` the instant an event drops; an unrefreshed
   registration expires (tested); a wake without the secret is refused (tested).
3. **The escalation ladder + `next_probe_after` / `429`.**
   *Done when:* a client ignoring `next_probe_after` takes a `429` (tested); the ladder resets to
   rung 0 on an event.
4. **Piggyback** — the inbox counter in every MCP tool response envelope.
   *Done when:* an active agent learns it was answered with no extra request.
5. **The waker: launch styles + agent config + the repo→path map.**
   *Done when:* `flowlio agent set …` switches the command; a fresh-style agent launches in the
   right directory; Claude resumes a known session.
6. **Delivery: `brew install flowlio` + the single `flowlio` command orchestrating DB(container) +
   engine + waker (self-host) and waker-only (hosted).**
   *Done when:* one command brings up a working self-host loop end to end; hosted runs the waker
   against prod with an account link.

**The end-to-end acceptance, from FLWL-8, unchanged:** an agent files an issue, dies, the sibling
answers, and **the loop closes with no human gesture** — while the probe-path SQL count stays
proportional to real events, never to time or agent count.

---

## 12. Decisions to record before or during the build

Two things here are decisions, not code, and per the project’s process they must be written to the
memory register (`remember`, with `supersedes` where they touch an existing one) and copied into
`docs/decisions.md`:

1. **Agent-agnostic relaxes FLWL-8’s Claude-only assumption** (resume stays Claude-specific; fresh
   launch is the generic path). Update the FLWL-8 card to point at this document.
2. **The installer / packaging shape** — the “own decision” DECISION-setup.md deferred: `brew` +
   single `flowlio` command, DB in a managed container, the waker folded into `flowlio` rather than
   shipped as a second binary.

---

## 13. What NOT to redo

- The D55 cost model — §3. Kept whole, not reopened.
- Postgres only, no SQLite — D27. The managed container is Postgres 18.
- Loopback-bound compose ports — D38. The managed DB container follows it.
- No accounts / billing / customer-agent execution in this repo — D24.
- The per-repository token/address config files and marker-bounded writes — DECISION-setup.md.
- The embedded self-host UI is frozen until ~2026-08-19 (FLWL-62) — this plan is CLI/daemon only and
  does not touch it.
