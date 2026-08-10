<!--
Security vulnerability? Close this and use the Security tab instead — see SECURITY.md.
A pull request is a public description of the flaw with a proof of concept attached.
-->

## What this changes

<!-- One or two sentences, in the terms a user would use. What becomes true that was not? -->

## Why

<!-- The part a reviewer cannot reconstruct from the diff: what you measured, what you tried that
     did not work, which decision in docs/ this follows or contradicts. -->

Closes #

## How you know it works

<!-- Which test goes red without this change? If you checked it by mutation — broke the behaviour
     on purpose and watched the test fail — say which mutation. -->

## Checks

- [ ] `make check` — vet and unit tests
- [ ] `make lint` — golangci-lint and the eight structural guards
- [ ] `make test-integration` — if this touches SQL, a store, or a handler
- [ ] `// SOMMAIRE` blocks updated for every added, renamed or removed top-level declaration
      (`make sommaire` fixes the line numbers; the descriptions are yours)
- [ ] Everything the code carries is in English — comments, identifiers, error messages
- [ ] No new external dependency, or it was agreed in an issue first
- [ ] No path added to a guard's tolerated list

## Anything else

<!-- A breaking change, a migration, a decision you would like argued rather than merged. -->
