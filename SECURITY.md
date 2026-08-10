# Security policy

## Reporting a vulnerability

**Do not open a public issue, and do not open a pull request that fixes it.** A pull request is a
public description of the flaw with a proof of concept attached, published before anybody can
upgrade.

Report it privately, through GitHub:

> **[Open a private security advisory](https://github.com/Coddyum/flowlio-agents/security/advisories/new)**
> — the *Security* tab of this repository, then *Report a vulnerability*.

Only the maintainers see it. The thread stays private until a fix ships, and it gives you a private
fork to work in if you want to propose the patch yourself.

What helps, roughly in order of how much:

- what an attacker gets — read another repo's backlog, write into a board they may not reach,
  recover a token, deny service;
- the version (`flowlio version`, or the tag you built from) and whether it is Docker or not;
- the smallest sequence that reproduces it;
- anything you already know about why the guard that should have caught it did not.

Expect a first reply within a few days. This is a small project — that answer may be a question.

## What is in scope

The engine and everything shipped from this repository: the API, the CLI, the MCP server, the SQL
predicates that carry the scoping rules, the token lifecycle, and the `docker-compose.yml` a
self-hosted instance runs on.

Specific claims that are worth breaking, because the project states them:

- an agent token reaches exactly **one** project — never a sibling repo's backlog, never another
  team;
- **a refused issue is byte-for-byte indistinguishable from a project that does not exist**;
- the trust graph is **directed**, and `A → B` grants nothing to `B → A`;
- **team scoping lives inside the queries**, not only in handlers;
- tokens are stored **SHA-256 hashed**, and no secret is printed, logged, or written into a
  repository;
- content written by a third-party repo comes back to an agent as **data, framed** — a framing whose
  seal is drawn from `crypto/rand` precisely so it cannot be closed by the author of the text.

Breaking any of those is a vulnerability, whatever the mechanism.

## What is out of scope

`docs/MODELE-DE-CONFIANCE.md` (French) is the authority on this, and it is worth reading before
writing a report. A security claim that is false is worse than one that is absent, so what the
project does *not* defend is written down as carefully as what it does. In particular:

- **Anything requiring access to the machine that runs the instance.** The admin credential is a
  `0600` file on that host; a reader of that file is meant to be able to administer the instance.
- **An instance deliberately published to a network.** Both ports bind to `127.0.0.1` by default,
  and the README says what publishing them costs. Choosing to publish Postgres is not a flaw in
  this repository.
- **An agent persuaded by an issue's content to do something.** Third-party text is framed as data
  and never presented as an instruction — that is the guarantee. What a model does with framed data
  it was told not to obey is a model's behaviour, not this engine's.
- `MODE=hosted` deployments operated by somebody else. Report those to whoever runs them.

## Supported versions

The latest release, and only it. This project is pre-1.0: fixes go into the next tag rather than
into a patched branch of an older one.

## Disclosure

A fix ships as a release, with a GitHub Security Advisory that names the versions affected. You are
credited unless you would rather not be — say so in the report.
