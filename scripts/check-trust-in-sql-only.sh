#!/usr/bin/env bash
# check-trust-in-sql-only.sh
# La décision de confiance vit dans une query SQL, jamais dans du Go.
#
# POURQUOI CE SCRIPT EXISTE. Le graphe de confiance autorise le canal inter-projets par un unique
# EXISTS dans le WHERE de CreateIssue (sql/queries/issues.sql). Ce placement n'est pas un goût :
# mesuré, le déplacer consomme le compteur du destinataire et pose un verrou sur sa ligne, ce qui
# offre à un émetteur non autorisé un canal d'écriture chez sa victime ET un déni de service
# ciblé sur un tiers légitime. Si un service, un handler ou un store a besoin de NOMMER la table,
# c'est que la décision a quitté la query — et aucune relecture ne rattrape ça de façon fiable.
#
# CE QUI EST TOLÉRÉ, ET POURQUOI :
#   - internal/database/  : code généré par sqlc, qui EST la query.
#   - *_test.go           : une fixture n'est pas une décision. Les tests d'intégration posent le
#                           graphe à la main, délibérément — le cacher dans un helper de création
#                           de projet masquerait la garantie que ces tests existent pour prouver.
#
# Usage : ./scripts/check-trust-in-sql-only.sh  (exit 1 si violation)
# Utilisé par `make lint`.

set -uo pipefail

TABLE="project_trust"

# grep -r rend 1 quand il ne trouve rien, ce qui est le cas nominal ici : `|| true` évite que
# `set -e` fasse échouer le script sur un succès.
hits="$(grep -rn --include='*.go' "${TABLE}" . \
	| grep -v '/internal/database/' \
	| grep -v '_test\.go:' \
	| grep -v '/scripts/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${hits}" ]]; then
	echo "VIOLATION : la table ${TABLE} est nommée dans du Go non généré et hors test."
	echo "${hits}" | sed 's/^/    /'
	echo
	echo "    La décision de confiance est un PRÉDICAT dans sql/queries/issues.sql (CreateIssue),"
	echo "    et l'administration vit dans sql/queries/trust.sql. Un .go qui nomme la table a"
	echo "    sorti la décision de la query — ce qui rouvre le canal que le volet 2 referme."
	exit 1
fi

echo "check-trust-in-sql-only: OK"
