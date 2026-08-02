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
	# Exclusions : tests, code généré sqlc, fichiers marqués DO NOT EDIT.
	case "$file" in
		*_test.go) continue ;;
		internal/database/*) continue ;;
	esac
	if head -n 3 "$file" | grep -q 'Code generated .* DO NOT EDIT'; then
		continue
	fi

	lines="$(wc -l < "$file" | tr -d ' ')"
	if (( lines > MAX_LINES )); then
		echo "TROP GROS (${lines} > ${MAX_LINES}) : ${file} → découper"
		status=1
	fi
	# Le périmètre vient de git, pas de `find .` : suivis + non suivis non ignorés.
	#
	# `find .` descendait dans .claude/worktrees/, où les agents montent des copies JETABLES du
	# dépôt. La cible échouait alors sur un fichier disparu entre le find et le head — un
	# garde-fou qui échoue par intermittence sur du code qui n'est pas le sien ne garde rien.
	# --others --exclude-standard garde les fichiers pas encore `git add`és, qui sont exactement
	# ceux qu'on vient d'écrire et qu'on veut vérifier avant de commiter.
done < <(git ls-files -z --cached --others --exclude-standard -- '*.go')

if [[ $status -eq 0 ]]; then
	echo "check-file-size: OK (limite ${MAX_LINES})"
fi
exit $status
