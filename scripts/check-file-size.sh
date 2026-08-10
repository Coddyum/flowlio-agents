#!/usr/bin/env bash
# check-file-size.sh
# Flags .go files over MAX_LINES (300 by default), excluding generated code and _test.go.
# A file that has grown too big is a signal to split it (SRP).
#
# Usage: ./scripts/check-file-size.sh  (exit 1 on a violation)

set -euo pipefail

MAX_LINES="${MAX_LINES:-300}"
status=0

while IFS= read -r -d '' file; do
	# Exclusions: tests, sqlc-generated code, files marked DO NOT EDIT.
	case "$file" in
		*_test.go) continue ;;
		internal/database/*) continue ;;
	esac
	if head -n 3 "$file" | grep -q 'Code generated .* DO NOT EDIT'; then
		continue
	fi

	lines="$(wc -l < "$file" | tr -d ' ')"
	if (( lines > MAX_LINES )); then
		echo "TOO BIG (${lines} > ${MAX_LINES}): ${file} → split it"
		status=1
	fi
	# The file list comes from git, not from `find .`: tracked plus untracked-not-ignored.
	#
	# `find .` descended into .claude/worktrees/, where agents mount THROWAWAY copies of the
	# repository. The target then failed on a file that disappeared between the find and the head —
	# a guard that fails intermittently on code that is not its own guards nothing.
	# --others --exclude-standard keeps the files not yet `git add`ed, which are exactly the ones
	# just written and the ones worth checking before committing.
done < <(git ls-files -z --cached --others --exclude-standard -- '*.go')

if [[ $status -eq 0 ]]; then
	echo "check-file-size: OK (limit ${MAX_LINES})"
fi
exit $status
