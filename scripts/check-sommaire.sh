#!/usr/bin/env bash
# check-sommaire.sh
# Vérifie que tout .go avec >= 2 déclarations top-level porte un bloc // SOMMAIRE synchronisé.
# Structure seule (présence + nombre de lignes de tableau == nombre de déclarations),
# PAS la qualité des descriptions.
#
# Usage :
#   ./scripts/check-sommaire.sh            → scanne tout le repo (make lint)
#   ./scripts/check-sommaire.sh <fichier>  → un seul fichier (hook PostToolUse)
# Exit 2 (bloquant) si manquant/désynchro, pour être utilisable en hook.

set -euo pipefail

MARKER='// SOMMAIRE (lire en premier, sauter directement au bon passage)'
status=0

check_one() {
	local file="$1"
	[[ "$file" == *.go ]] || return 0
	[[ -f "$file" ]] || return 0

	# Exclusions : généré sqlc, DO NOT EDIT, tests.
	case "$file" in
		*/internal/database/*) return 0 ;;
		*_test.go) return 0 ;;
	esac
	if head -n 3 "$file" | grep -q 'Code generated .* DO NOT EDIT'; then
		return 0
	fi

	# Déclarations top-level : lignes commençant par "func " ou "type ".
	local decls
	decls="$(grep -cE '^(func |type )' "$file" || true)"

	if (( decls < 2 )); then
		# Bloc interdit si < 2 déclarations : s'il est présent, désynchro.
		if grep -qF "$MARKER" "$file"; then
			echo "SOMMAIRE en trop (${decls} déclaration) : ${file} → retirer le bloc"
			echo "    → /sommaire ${file}"
			status=2
		fi
		return 0
	fi

	# >= 2 déclarations : marqueur obligatoire.
	if ! grep -qF "$MARKER" "$file"; then
		echo "SOMMAIRE manquant (${decls} déclarations) : ${file}"
		echo "    → /sommaire ${file}"
		status=2
		return 0
	fi

	# Compter les lignes de tableau du bloc : lignes '// | ... | ... | ... |'
	# hors ligne d'en-tête (| Élément |) et séparateur (|---|).
	local rows
	rows="$(grep -E '^// \|' "$file" \
		| grep -vE '^// \| *Élément' \
		| grep -vE '^// \|[-| ]+\|?[[:space:]]*$' \
		| grep -c '|' || true)"

	if (( rows != decls )); then
		echo "SOMMAIRE désynchronisé : ${file} (${rows} lignes de tableau vs ${decls} déclarations)"
		echo "    → /sommaire ${file}"
		status=2
	fi
}

if [[ $# -gt 0 ]]; then
	for f in "$@"; do check_one "$f"; done
else
	# Le périmètre vient de git, pas de `find .` : suivis + non suivis non ignorés.
	#
	# `find .` descendait dans .claude/worktrees/, où les agents montent des copies JETABLES du
	# dépôt : la cible échouait sur un fichier disparu entre le find et la lecture. Un garde-fou
	# qui échoue par intermittence sur du code qui n'est pas le sien ne garde rien.
	while IFS= read -r -d '' f; do check_one "$f"; done \
		< <(git ls-files -z --cached --others --exclude-standard -- '*.go')
fi

if [[ $status -eq 0 ]]; then
	echo "check-sommaire: OK"
fi
exit $status
