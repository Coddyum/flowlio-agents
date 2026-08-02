#!/usr/bin/env bash
# check-file-size.sh
# Flag les fichiers .go > MAX_LINES (défaut 300), hors code généré et _test.go.
# Un fichier trop gros = signal de découpage (SRP).
#
# Usage : ./scripts/check-file-size.sh  (exit 1 si dépassement)

set -euo pipefail

MAX_LINES="${MAX_LINES:-300}"
status=0

while IFS= read -r -d '' file; do
	# Exclusions : code généré sqlc, fichiers marqués DO NOT EDIT.
	case "$file" in
		*/internal/database/*) continue ;;
	esac
	if head -n 3 "$file" | grep -q 'Code generated .* DO NOT EDIT'; then
		continue
	fi

	lines="$(wc -l < "$file" | tr -d ' ')"
	if (( lines > MAX_LINES )); then
		echo "TROP GROS (${lines} > ${MAX_LINES}) : ${file} → découper"
		status=1
	fi
done < <(find . -type f -name '*.go' ! -name '*_test.go' -print0)

if [[ $status -eq 0 ]]; then
	echo "check-file-size: OK (limite ${MAX_LINES})"
fi
exit $status
