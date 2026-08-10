#!/usr/bin/env bash
# hook-go-postedit.sh
# PostToolUse hook (Edit|Write). Reads the hook's JSON on stdin, extracts the edited file.
# If it is a .go: go build + go vet + the structural guards. Exit 2 = blocking (feedback to Claude).
#
# Wired in .claude/settings.json. Does nothing when the tool did not touch a .go file.

set -uo pipefail

cd "$(dirname "$0")/.."

payload="$(cat)"

# Extract the file path from the hook payload (tool_input.file_path).
file="$(printf '%s' "$payload" | sed -nE 's/.*"file_path"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -n1)"

[[ -n "$file" && "$file" == *.go ]] || exit 0

# An edit outside this repository is not ours to judge. Without this the hook fires on sibling
# repositories whenever the session's working directory is elsewhere: it then runs `go build`
# on THIS module and demands a sommaire on THEIR file. Observed live on 2026-08-05, blocking an
# edit to a flowlio-core file from a session rooted here.
repo_root="$(pwd)"
rel="${file#"$repo_root"/}"
[[ "$rel" != /* ]] || exit 0

fail=0

# 1. Build and vet over the whole module (an error elsewhere has to block too).
if ! go build ./... 2>&1; then
	echo "hook: go build fails — fix it before going on." >&2
	fail=2
fi
if ! go vet ./... 2>&1; then
	echo "hook: go vet fails." >&2
	fail=2
fi

# 2. Cross-feature imports (when the file sits under a feature).
if [[ "$file" == *internal/feature/* ]]; then
	if ! ./scripts/check-cross-feature-imports.sh >&2; then
		fail=2
	fi
fi

# 3. The edited file's sommaire: line numbers are resynchronised automatically, only a missing or
#    extra table row is left to fix by hand.
./scripts/sync-sommaire-lines.sh "$file" >&2 || true
if ! ./scripts/check-sommaire.sh "$file" >&2; then
	fail=2
fi

exit $fail
