# GitHub settings — what protects this repository

Maintainer document. It lists every setting that is not a default, what each one actually refuses,
and the command to apply it. Written down rather than clicked once and forgotten, because a
protection nobody can name is a protection nobody notices losing.

State when this was written: **public, personal account, `main` default, no branch protection, no
ruleset, secret scanning off.** Everything below was a change.

**Applied 2026-08-10**, except where a row says otherwise. Section 6 is how you check it is all
still true.

---

## First, what is *not* the risk

The instinct is "stop random people merging into my repo". On a public repository that cannot
happen and never could: **merging requires write access**, and nobody has it but you. An outside
contributor can fork and open a pull request, and that is the end of what they can do on their own.

The real exposures are different, and each has its own setting further down:

| Exposure | What it looks like | Fixed by |
| --- | --- | --- |
| An accidental push straight to `main` | a force-push that rewrites history, a commit that skipped CI | the `main` ruleset |
| A tag pushed by mistake | `v*` triggers the release workflow — binaries with your name on them | the tag ruleset |
| A pull request from a fork running workflows | a workflow edited in the fork, reading secrets, exfiltrating them | the Actions settings |
| A secret committed by anyone, including you | a `DATABASE_URL` in a `.env` that slipped past `.gitignore` | secret scanning + push protection |
| A compromised token acting as you | same as the first row, but deliberate | the ruleset, applied to admins too |

The fourth one is the likeliest, by a distance.

---

## 1. The `main` ruleset

Rulesets, not the legacy "branch protection rules" screen. Same guarantees, they can target tags as
well as branches, and they are the surface GitHub is still developing.

**Settings → Rules → Rulesets → New ruleset → New branch ruleset.**

| Setting | Value | What it refuses |
| --- | --- | --- |
| Name | `main` | — |
| Enforcement status | **Active** | *Evaluate* only logs, it does not block |
| Target branches | Include **default branch** | — |
| Restrict deletions | ✅ | deleting `main` |
| Block force pushes | ✅ | rewriting published history |
| Require a pull request before merging | ✅ | a commit reaching `main` without a diff anybody could read |
| — Required approvals | **0** while you are alone (see below) | — |
| — Dismiss stale approvals when new commits are pushed | ✅ | an approval of code that has since changed |
| — Require conversation resolution | ✅ | merging over an unanswered review comment |
| Require status checks to pass | ✅ | merging a red build |
| — Required check | the CI job (see below) | — |
| — Require branches to be up to date | ✅ | two PRs that are each green and broken together |
| Require linear history | ✅ | optional; keeps `git log --oneline` readable |

**Why zero required approvals, on purpose.** GitHub does not let you approve your own pull request.
With one required approval and one maintainer, every one of your own pull requests is unmergeable
until you bypass the rule you just set — and a rule bypassed daily stops being read at all. Zero
approvals still buys the pull request itself, the status checks, and the resolved conversations,
and it costs an outside contributor exactly nothing they could have had anyway.

**The day a second person gets write access**, raise it to 1 and tick *Require review from Code
Owners* — [.github/CODEOWNERS](../.github/CODEOWNERS) already names every path. Then add a bypass
for **Repository admin** so you are not locked out of an urgent fix, and know that you have.

**The required check is the one part not applied yet, and it is on purpose.** The name has to match
exactly: it is the job's `name:` in [.github/workflows/ci.yml](../.github/workflows/ci.yml) — today
`build, garde-fous et tests`. A required check whose name carries a typo never runs and never
blocks, which looks identical to being protected.

So the ruleset went up without it. Open the first pull request, let CI run, then
**Settings → Rules → `main` → Require status checks** and pick the check from the list GitHub
offers rather than typing it.

<details>
<summary>The same thing over the API</summary>

```bash
gh api -X POST repos/Coddyum/flowlio-agents/rulesets --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_linear_history" },
    { "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["squash"]
      } },
    { "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [ { "context": "build, garde-fous et tests" } ]
      } }
  ]
}
JSON
```

Check what is live with `gh api repos/Coddyum/flowlio-agents/rulesets`, and what a given ruleset
contains with `gh api repos/Coddyum/flowlio-agents/rulesets/<id>`.
</details>

---

## 2. The tag ruleset — the one people forget

**A tag matching `v*` publishes binaries.** `.github/workflows/release.yml` fires on it, builds
`flowlio` and `flowlio-api` for four platforms, and creates a release. Today anything able to push
a tag can start that, and a tag is easier to push by accident than a branch — `git push --tags`
does it without naming one.

The workflow already refuses a tag that does not descend from `main`, and the release is created as
a **draft**. Both are real, and neither stops the tag from existing or the build from running.

**Settings → Rules → Rulesets → New ruleset → New tag ruleset.**

| Setting | Value |
| --- | --- |
| Name | `release tags` |
| Enforcement | **Active** |
| Target tags | Include by pattern: `v*` |
| Restrict updates | ✅ — a published tag never moves |
| Restrict deletions | ✅ — an existing release keeps pointing at real code |

<details>
<summary>The same thing over the API</summary>

```bash
gh api -X POST repos/Coddyum/flowlio-agents/rulesets --input - <<'JSON'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "rules": [ { "type": "update" }, { "type": "deletion" } ]
}
JSON
```
</details>

**No bypass actor, deliberately — the rule applies to you too.** On a repository where one person
has write access, an admin bypass would exempt the only account capable of triggering the rule, and
protect against nobody. The threat here is your own `git push --tags`, not a stranger's.

The price: a tag pushed onto the wrong commit cannot be moved or deleted. You drop the **draft
release** and cut `v0.4.1`. That is the right way round — a version number that once pointed at
code is a fact, and moving it means two people can build different trees from the same tag.

---

## 3. Actions — where a fork could actually hurt you

**Settings → Actions → General.**

| Setting | Value | Why |
| --- | --- | --- |
| Fork pull request workflows → *Require approval for* | **All external contributors** | The default only holds first-time contributors. One merged typo fix makes somebody "not first-time" forever, and the next pull request runs on your runners without a click |
| Workflow permissions | **Read repository contents and packages permissions** | The default write token is handed to every step of every workflow. `release.yml` asks for what it needs, where it needs it — that is what the `permissions:` blocks in it are |
| Allow GitHub Actions to create and approve pull requests | **off** | An approval by a bot is not a review, and it satisfies a required-approval rule as if it were |

The last two were already right; the first was on the default and was changed. All three are
readable and settable over the API, which the GitHub documentation does not make obvious:

```bash
gh api repos/Coddyum/flowlio-agents/actions/permissions/workflow
# → {"default_workflow_permissions":"read","can_approve_pull_request_reviews":false}
gh api -X PUT repos/Coddyum/flowlio-agents/actions/permissions/fork-pr-contributor-approval \
  -f approval_policy=all_external_contributors
```

The workflows themselves already do the right thing: `ci.yml` is `permissions: contents: read` and
needs no secret, so it runs unchanged on a fork.

### Pinning actions to a SHA — not done, and why it is two steps

`ci.yml` and `release.yml` reference `goreleaser/goreleaser-action@v6` and
`golangci/golangci-lint-action@v9`. **A tag is mutable**: whoever owns that repository can move `v6`
onto new code, and your next release builds whatever it now points at. `actions/*` are GitHub's own
and carry less of this risk; those two are not.

Pinning looks like:

```yaml
uses: goreleaser/goreleaser-action@9ed2f89a662bf1735a48bc8557fd212fa902bebf # v6.1.0
```

GitHub can also **enforce** it, and the setting is visible today:

```bash
gh api repos/Coddyum/flowlio-agents/actions/permissions
# → {"enabled":true,"allowed_actions":"all","sha_pinning_required":false}
```

**Do not turn `sha_pinning_required` on before pinning them.** Every workflow referencing a tag
stops running the moment it flips, which means CI red on every pull request and a release that
cannot build. Pin the four `uses:` lines first, watch one CI run go green, then enforce.

---

## 4. Security features

**Settings → Advanced Security** (the wording moves around; on a public repository these are free).

| Setting | Value | What it does |
| --- | --- | --- |
| Private vulnerability reporting | **on** | Gives [SECURITY.md](../SECURITY.md) the channel it points at. Without it, that link 404s and reports arrive as public issues |
| Secret scanning | **on** | Finds a committed credential after the fact, including in old history |
| Push protection | **on** | Refuses the push that carries one. This is the one that saves you |
| Dependabot alerts | **on** | Tells you a dependency has a known vulnerability |
| Dependabot security updates | **on** | Opens a pull request for exactly those |
| Dependabot **version** updates | **off** | Would open a pull request for every release of every dependency, and the ones it opens on the day of publication are the ones a supply-chain compromise ships in |

That last row is a deliberate disagreement with the default advice, and it matches how this project
treats packages: nothing that fresh gets installed. Security updates are different — they are
scoped to a known vulnerability, and they are opened as a pull request you read, not applied.

```bash
gh api -X PATCH repos/Coddyum/flowlio-agents \
  -f 'security_and_analysis[secret_scanning][status]=enabled' \
  -f 'security_and_analysis[secret_scanning_push_protection][status]=enabled'
gh api -X PUT repos/Coddyum/flowlio-agents/private-vulnerability-reporting
gh api -X PUT repos/Coddyum/flowlio-agents/vulnerability-alerts
gh api -X PUT repos/Coddyum/flowlio-agents/automated-security-fixes
```

**Applied 2026-08-10.** All five confirmed live.

### Two things secret scanning does not do here, and the gap it leaves

`secret_scanning_non_provider_patterns` and `secret_scanning_validity_checks` are **not** free on a
public repository — they belong to GitHub Secret Protection. Do not retry them over the API: the
`PATCH` answers `200 OK` and leaves the value at `disabled`, so it reads as applied and is not.
Measured 2026-08-10, both together and one at a time.

What that leaves uncovered is worth naming, because it is the only kind of secret this repository
actually handles. The default scan recognises **known provider formats** — AWS keys, Stripe tokens,
GitHub tokens. A Postgres DSN is nobody's token:

```
postgres://user:password@ep-xxx.REGION.aws.neon.tech/flowlio?sslmode=require
```

That form goes through untouched, and it is exactly what `DATABASE_URL_PROD` holds. `.gitignore` is
the only thing standing in front of it, so the `.env` rules in it are load-bearing rather than
tidy — `.env`, `.env.*`, and `!.env.example` to let the template through.

---

## 5. General repository settings

**Settings → General.**

| Setting | Value | Why |
| --- | --- | --- |
| Automatically delete head branches | **on** | Merged branches accumulate and nothing ever cleans them |
| Allow squash merging | **on**, and it is the only one | One commit per pull request. With *Require linear history* in the ruleset, `git log --oneline` on `main` is the list of merged pull requests and nothing else |
| Allow merge commits | **off** | It is what *Require linear history* refuses anyway; leaving it offered would put a button on `main` that the ruleset then rejects |
| Allow rebase merging | **off** | It lands several commits from one branch, so the pull request stops being a unit in the log — the unit a reviewer actually approved |
| Squash commit title / message | `PR_TITLE` / `PR_BODY` | The pull request's own title and description become the commit. That is what makes the release notes readable, since they are generated from these subjects |
| Description and website | filled in | Both were empty. It is the only text most visitors read |
| Topics | `mcp`, `ai-agents`, `claude-code`, `golang`, `project-management`, `self-hosted`, `model-context-protocol` | How anybody finds this without already knowing the name |

```bash
gh api -X PATCH repos/Coddyum/flowlio-agents \
  -F delete_branch_on_merge=true \
  -F allow_squash_merge=true -F allow_merge_commit=false -F allow_rebase_merge=false \
  -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=PR_BODY \
  -f description='A shared backlog for your AI coding agents — CLI and MCP, self-hosted, no LLM inside' \
  -f homepage='https://flowlio.me'

gh api -X PUT repos/Coddyum/flowlio-agents/topics \
  -f 'names[]=mcp' -f 'names[]=ai-agents' -f 'names[]=claude-code' -f 'names[]=golang' \
  -f 'names[]=project-management' -f 'names[]=self-hosted' -f 'names[]=model-context-protocol'
```

**A consequence worth stating once:** the pull request title is now the commit subject on `main`,
so the Conventional Commits rule in `CONTRIBUTING.md` applies to the *title of the pull request*,
not only to the commits inside it.

---

## 6. Checking it is all still there

Protections drift, mostly by being turned off for one urgent thing and never turned back on. Three
commands, worth running before a release:

```bash
gh api repos/Coddyum/flowlio-agents/rulesets --jq '.[] | "\(.name)\t\(.target)\t\(.enforcement)"'
gh api repos/Coddyum/flowlio-agents --jq '.security_and_analysis, .delete_branch_on_merge'
gh api repos/Coddyum/flowlio-agents/actions/permissions/workflow
gh api repos/Coddyum/flowlio-agents/actions/permissions/fork-pr-contributor-approval
```

Expected:

```
main            branch  active
release tags    tag     active
secret_scanning: enabled     secret_scanning_push_protection: enabled
dependabot_security_updates: enabled     delete_branch_on_merge: true
default_workflow_permissions: read       can_approve_pull_request_reviews: false
approval_policy: all_external_contributors
```

`secret_scanning_non_provider_patterns` and `secret_scanning_validity_checks` read `disabled` and
stay there — see section 4. They are the two rows that are meant to be red.

---

## Known inconsistency, not fixed here

The two workflow files are written in French — job names, step names, comments — in a repository
whose rule is that everything the code carries is English. It is stock to clear, like the rest.

Renaming the CI job is not a free edit once a ruleset requires it by name: the required check would
silently stop matching, and the branch would go unprotected while looking protected. Do the two
together — rename the job, then update the required check — or not at all.
