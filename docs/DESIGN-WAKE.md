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
- **How the woken agent authenticates the MCP.** A hosted repo's committed `.mcp.json` points Claude
  at the remote engine surface and leaves auth to an OAuth flow — which a `claude -p` the waker
  launches cannot run (no browser). So for a hosted Claude the waker writes a second, host-local MCP
  config beside the credential (`repos/hosted/<id>.mcp.json`, 0600) carrying the account token in an
  `Authorization` header, and launches with `--mcp-config <that> --strict-mcp-config` so Claude loads
  THAT server and ignores the repo's OAuth one. The account token is the same one `flowlio login`
  filed and the waker already presents to the relay; `?repo=<id>` scopes it, exactly as the relay
  does. Interactive sessions in the repo never read this file and keep using OAuth. The file is
  rewritten on every launch, so a rotated token or a moved address takes effect on the next wake. It
  lives in `cmd/flowlio/waker_mcp.go`.

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

  **Hosted records key by the core id, not by the repo key.** A hosted machine holds no project slug,
  so every hosted repo sits under the one `repos/hosted/` directory. Keyed by key alone, two accounts
  that each named a repo `CORE` would write the same `hosted/CORE.json`, and the second
  `flowlio connect --id` would silently bury the first (seen on 2026-08-12). So a hosted record — the
  only kind carrying a `repo_id` — is filed under `repos/hosted/<repo-id>.json`, the one name a hosted
  machine has that is unique per repository. Its `.session` file follows the same path. Self-host
  keeps `<project>/<REPO>.json`: there the slug is real, so the key never collides. The rule lives in
  `credentials.RepoRecordPath`.

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
| **Actionable gate on the probe** (FLWL-85) | `head > watermark` means "the journal moved", not "there is work". A wake is a full session boot, so the probe confirms **new actionable work** before it says yes — never a launch for a closed issue or a sibling's traffic. §15. |
| **Wake watermark, not the read cursor** (FLWL-86) | The gate rides a per-project **wake watermark** the probe alone advances, never the token's inbox cursor which `check_inbox` moves on a mere read. Gating on the cursor left an issue an agent *looked at without answering* unwakeable — the exact inverse of FLWL-85. §16. |
| **Failure circuit-breaker** (FLWL-85) | The window bounds a burst, not a wall: a repo whose launches keep failing was retried every cadence for an hour into the account session limit. Consecutive failures now earn an exponential backoff; a recognised session limit blocks the repo outright. §15. |

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

---

## 14. The effort tier — the sender declares rigour, the receiver picks the model (FLWL-84)

A wake is a full session boot, and it need not be an Opus one. Answering "which front framework?"
warrants a lookup; a careful architecture question warrants the strongest model. So the issue's author
declares **how much rigour** answering warrants, and the receiver turns that into a model for **its**
agent.

- The tier is `create_issue`'s optional `effort`: `low | standard | high | max`, default `standard`.
  It is an **abstract intent, never a model name** — the author does not know whether Claude, codex or
  opencode answers on the other side, so naming `opus` would couple the repos. Vocabulary lives in
  `internal/pkg/effort`; the DB stores it nullable (migration `000016`, NULL = unspecified = standard).
- The tier travels **both transports**: the probe returns `suggested_effort` (poll, forwarded verbatim
  by flowlio-core's relay), and the local push carries it in the body (self-host). It is computed
  where the actionable read already runs (§15) — the max tier among the work a wake will act on — so it
  costs nothing extra.
- The **receiver maps and clamps**. `internal/pkg/waker` maps a tier to a model — for Claude,
  `low→haiku, standard→sonnet, high/max→opus`; codex/opencode carry no ladder yet (FLWL-85 follow-up)
  and launch at their default. The daemon clamps every tier to `FLOWLIO_WAKE_MAX_EFFORT` (default
  `max`, no cap): **sender proposes, receiver disposes.**
- **Security** (`docs/MODELE-DE-CONFIANCE.md`): the tier is consumed by the daemon as a launch
  parameter, never reaches the agent as an instruction, so it does not cross the untrusted seal. But a
  hostile sender pinning `max` on every issue is a **cost-amplification** vector — hence the clamp is
  not optional.

## 15. The probe tells the truth, and the breaker catches a wall (FLWL-85)

2026-08-12: the hosted waker relaunched a repo every 60s for 90 minutes with an **empty inbox**,
burning ~11% of a quota on empty Opus boots, then hammered the account session limit for another hour.
Two faults, both here now.

**The probe tested the wrong thing.** `HasWork = head > cursor` answers "an event I have not accounted
for exists" — not "a question awaits an answer". Events include `issue.closed` and, on a team-wide
head, a sibling's entire traffic. The old comment "a wasted wake is cheap" was false: a wake is a full
session. The fix is a **two-step probe**:

1. cheap gate — `head > cursor`? Zero SQL when idle (unchanged; D55's 100-empty-probes test stays
   green). (The boundary later became the wake watermark, not the cursor — §16 — but the two-step
   shape and its cost model are as written here.)
2. only when the gate passes — one indexed read: is there **new actionable work** (a new incoming open
   issue, a new answer to mine, a newly unblocked task; **not** `in_progress`, **not** `closed`)? No ⇒
   `HasWork=false`, **no launch**. The read runs only on the has-work path, and the ladder climbs on a
   non-actionable bump, so it fires a handful of times then rarely — an occasional query, never a
   session. This is the one change to D55's "a probe is pure memory": a *has-work* probe now costs one
   query; the idle poll never does.

**The window did not notice a wall.** The relaunch cap bounds a burst but happily allows N launches per
window, every window, even when every one fails. The cap is now also a **circuit-breaker**: consecutive
failures earn an exponential backoff (`internal/pkg/waker`, `RecordOutcome`), and a recognised account
session limit (`sessionLimited` reads the agent-log tail) blocks the repo for a fixed pause
(`Block`). A wall is hit a handful of times, not once a minute for an hour.

**Delivery caveat.** Both fixes live in the engine and the daemon binary. The hosted engine is pinned
per image and lags (D29, FLWL-83): until an image can fetch the current binary, prod keeps the
team-wide head, and the hosted waker stays down.

## 16. The gate rides a wake watermark, not the read cursor (FLWL-86)

2026-08-13: the inverse of §15. An incoming issue sat open ~10 min on a session Maxence had launched,
and the waker never relaunched an agent for it — restarting `flowlio` changed nothing.

**The wake decision rode the inbox read cursor.** The gate was `head > cursor` and the confirming read
measured "new" from that same `cursor` — `token_cursors.last_event_id`. But `check_inbox` advances the
cursor on a **mere read** (`inbox/service/check.go` → `Advance`). So the instant any session, manual or
woken, looked at the inbox without answering, the cursor sat at the head, the open issue's event was no
longer "new", and the gate went false **forever** — the issue never woke anyone again. It is the exact
mirror of FLWL-85: that card stopped waking on movement-that-is-not-work; this one stopped
work-that-was-merely-looked-at from ever waking.

**The fix: a per-project wake watermark, decoupled from the cursor.** A third probe scalar
(`internal/core/probe`, alongside the head and the cursor), it is the head the probe last made a launch
decision on. The gate becomes `head > watermark`; the confirming read measures "new" from the
watermark; on a clean actionable read the watermark advances to the head just decided on. Two things it
must both hold, and does:

- **A looked-at issue still wakes.** `check_inbox` advances the cursor, never the watermark — only the
  probe advances the watermark — so a session that read an open issue without answering leaves the
  watermark behind it, and the waker relaunches to finish the work.
- **The void-loop FLWL-85 closed stays closed.** The watermark advances the moment the probe decides
  to wake, so the *same* standing work does not relaunch every probe; a new event lifts the head above
  the watermark and earns a fresh wake. Loop-safety no longer depends on the woken agent successfully
  running `check_inbox` — before, an agent that crashed before that call left the cursor unmoved and
  looped.

The watermark is in-cache and non-durable, like the pacing ladder: a cold engine reads it as 0 and
re-decides standing work once (a bounded burst on restart, erring towards a wake, never a miss). The
**piggyback** (`core/services.go`) keeps comparing `head > cursor`: nudging an *active* agent to
re-read its inbox is a different question from deciding to boot a *dead* one, and the cursor is right
for the first. Same delivery caveat as §15: engine-side, so it reaches hosted only once FLWL-83 lifts.

## 17. The woken agent must be able to ACT, not only answer (FLWL-87)

2026-08-13, right after §16 made the wake fire: the waker relaunched Claude for an open issue, the run
exited clean (`done — 1m22s`), and nothing happened — no answer, no code. The agent log told the
story: *"the edit tool keeps getting denied — no permission granted for that file."*

**The launch pre-approved the MCP server and nothing else.** The Claude preset ran
`claude -p … --allowedTools mcp__flowlio-agents`. A `-p` session is non-interactive: it cannot approve
a tool at the prompt, and `--allowedTools` was the *whole* allow-list. So the woken agent could
`check_inbox` and `answer_issue` over MCP, but every `Edit` / `Write` / `Bash` was denied. An issue
that asks for a code change — most of them — stalls on the first write. The `WakePrompt` says "act on
them", a promise the launch could not keep.

**The fix: the woken Claude runs `--permission-mode bypassPermissions`.** Closing the loop with no
human means the agent must act on the repo exactly as an AFK interactive agent would — edit files, run
the build, commit. Half-autonomy (answer but not implement) is not the product.

**Why this is safe, and where the real guard is.** The guard was never the permission scope — it is
that anything a *sibling* repo wrote reaches the agent sealed as untrusted DATA
(`cmd/flowlio/mcp_untrusted.go`, `docs/MODELE-DE-CONFIANCE.md`, the indistinguishable-refusal design).
A hostile issue cannot become an instruction, so it cannot turn this autonomy into an injected
command whatever the permission mode. Narrowing tools bought nothing against that threat and broke the
product against the ordinary one (the repo's own real work). The autonomy is the same a human grants
an agent they leave running; the untrusted seal is what makes leaving *this* one running safe.

**Delivery — waker-side, not engine-side.** Unlike §15/§16, this lives in the local `flowlio` binary
(`internal/pkg/waker`), not the engine. It reaches a user through `brew upgrade flowlio` (the tap
builds from the source tarball, present as soon as the tag is pushed), **not** a flowlio-core
redeploy. Shipped in v1.1.2 alongside the §16 engine fix.
