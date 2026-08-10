#!/usr/bin/env bash
# check-sommaire.sh
# Checks that every .go file with >= 2 top-level declarations carries a synchronised // SOMMAIRE
# block. Structure only (present + as many table rows as declarations), NOT the quality of the
# descriptions.
#
# Usage:
#   ./scripts/check-sommaire.sh          → scan the whole repository (make lint)
#   ./scripts/check-sommaire.sh <file>   → one file (PostToolUse hook)
# Exit 2 (blocking) when missing or out of sync, so it can be used as a hook.
#
# MARKER AND THE HEADER PATTERN STAY IN FRENCH, and that is not an oversight. They are compared
# literally against the text of every .go file in the repository, and the same two strings are used
# by flowlio-core and Flowlio. Translating them here would fail this guard on 263 files at once, in
# three repositories. The descriptions inside a block are English like everything else.

set -euo pipefail

MARKER='// SOMMAIRE (lire en premier, sauter directement au bon passage)'
status=0

check_one() {
	local file="$1"
	[[ "$file" == *.go ]] || return 0
	[[ -f "$file" ]] || return 0

	# Exclusions: sqlc-generated, DO NOT EDIT, tests.
	case "$file" in
		*/internal/database/*) return 0 ;;
		*_test.go) return 0 ;;
	esac
	if head -n 3 "$file" | grep -q 'Code generated .* DO NOT EDIT'; then
		return 0
	fi

	# Top-level declarations: lines starting with "func " or "type ".
	local decls
	decls="$(grep -cE '^(func |type )' "$file" || true)"

	if (( decls < 2 )); then
		# The block is forbidden below 2 declarations: if it is there, it is out of sync.
		if grep -qF "$MARKER" "$file"; then
			echo "SOMMAIRE not wanted (${decls} declaration): ${file} → remove the block"
			echo "    → /sommaire ${file}"
			status=2
		fi
		return 0
	fi

	# >= 2 declarations: the marker is mandatory.
	if ! grep -qF "$MARKER" "$file"; then
		echo "SOMMAIRE missing (${decls} declarations): ${file}"
		echo "    → /sommaire ${file}"
		status=2
		return 0
	fi

	# Count the block's table rows: lines of the form '// | ... | ... | ... |', excluding the header
	# row (| Élément |) and the separator (|---|).
	local rows
	rows="$(grep -E '^// \|' "$file" \
		| grep -vE '^// \| *Élément' \
		| grep -vE '^// \|[-| ]+\|?[[:space:]]*$' \
		| grep -c '|' || true)"

	if (( rows != decls )); then
		echo "SOMMAIRE out of sync: ${file} (${rows} table rows vs ${decls} declarations)"
		echo "    → /sommaire ${file}"
		status=2
	fi
}

if [[ $# -gt 0 ]]; then
	for f in "$@"; do check_one "$f"; done
else
	# The file list comes from git, not from `find .`: tracked plus untracked-not-ignored.
	#
	# `find .` descended into .claude/worktrees/, where agents mount THROWAWAY copies of the
	# repository: the target failed on a file that disappeared between the find and the read. A
	# guard that fails intermittently on code that is not its own guards nothing.
	while IFS= read -r -d '' f; do check_one "$f"; done \
		< <(git ls-files -z --cached --others --exclude-standard -- '*.go')
fi

if [[ $status -eq 0 ]]; then
	echo "check-sommaire: OK"
fi
exit $status
