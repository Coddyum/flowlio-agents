#!/usr/bin/env bash
# check-cross-feature-imports.sh
# Interdit qu'une feature en importe une autre directement.
# Une feature ne peut importer QUE sa propre sous-arbo internal/feature/<self>/...
# Toute dépendance inter-features passe par FeatureRegistry / CoreServices.
#
# Usage : ./scripts/check-cross-feature-imports.sh  (exit 1 si violation)
# Utilisé par `make lint` et le hook PostToolUse.

set -euo pipefail

FEATURE_ROOT="internal/feature"
# Module path Go (ex: github.com/you/project) — extrait de go.mod.
MODULE_PATH="$(awk '/^module /{print $2; exit}' go.mod)"

if [[ -z "${MODULE_PATH}" ]]; then
	echo "check-cross-feature-imports: module path introuvable dans go.mod" >&2
	exit 1
fi

status=0

# Pour chaque fichier .go sous une feature, repérer les imports d'une AUTRE feature.
while IFS= read -r -d '' file; do
	# feature à laquelle appartient le fichier (segment après internal/feature/)
	self="$(printf '%s\n' "$file" | sed -E "s#^.*${FEATURE_ROOT}/([^/]+)/.*#\1#")"

	# imports pointant vers internal/feature/<other>
	while IFS= read -r imported; do
		other="$(printf '%s\n' "$imported" | sed -E "s#^.*${FEATURE_ROOT}/([^/\"]+).*#\1#")"
		if [[ -n "${other}" && "${other}" != "${self}" ]]; then
			echo "VIOLATION import inter-feature : ${file}"
			echo "    feature '${self}' importe feature '${other}' (${imported})"
			echo "    → passer par FeatureRegistry.Get(\"${other}\") ou CoreServices"
			status=1
		fi
	done < <(grep -oE "\"${MODULE_PATH}/${FEATURE_ROOT}/[^\"]+\"" "$file" || true)
done < <(find "${FEATURE_ROOT}" -type f -name '*.go' ! -name '*_test.go' -print0 2>/dev/null)

if [[ $status -eq 0 ]]; then
	echo "check-cross-feature-imports: OK"
fi
exit $status
