#!/usr/bin/env bash
# check-overview-scope.sh
# overview est le seul endroit du dépôt qui lit des lignes métier sans prédicat de projet.
#
# POURQUOI CE SCRIPT EXISTE. Partout ailleurs, une query oubliant son scope se fait rattraper par
# le fait que le projet vient du token. Ici la règle est INVERSE — team_id seul — et il n'existe
# donc plus de garde-fou implicite. Trois choses peuvent mal tourner, et aucune ne se voit à la
# relecture d'un diff :
#
#   1. une query d'overview qui oublie `team_id = @team_id` lit TOUTE la base, toutes teams
#      confondues ;
#   2. un INSERT/UPDATE/DELETE qui s'y glisse rend mutable une surface conçue en lecture pure,
#      derrière une route qu'aucun test d'écriture ne couvre ;
#   3. et surtout : `internal/database` est importable de partout. Un contributeur qui trouve
#      `OverviewIssueDebts` pratique et l'appelle depuis `issue/store` obtient une fuite complète
#      — et check-cross-feature-imports.sh ne voit RIEN, parce que `internal/database` n'est pas
#      une feature.
#
# La troisième est le vrai risque permanent. Ce script ne le rend pas nul, il le rend bruyant.
#
# Usage : ./scripts/check-overview-scope.sh  (exit 1 si violation)
# Utilisé par `make lint`.

set -uo pipefail

QUERIES="sql/queries/overview.sql"
FEATURE_DIR="internal/feature/overview/"

# OverviewTeamBySlug est l'exception NOMMÉE : elle PRODUIT le scope à partir d'un slug, donc elle
# ne peut pas le porter. Toute autre exception ajoutée ici est une décision de sécurité.
SCOPE_EXEMPT="OverviewTeamBySlug"

status=0

if [[ ! -f "${QUERIES}" ]]; then
	echo "check-overview-scope: ${QUERIES} introuvable"
	exit 1
fi

# --- 1. Chaque bloc `-- name:` porte team_id = @team_id -------------------------------------
#
# Le découpage se fait sur les lignes `-- name:` : le corps d'une query est tout ce qui la suit
# jusqu'à la prochaine. Compter les occurrences sur le fichier entier ne dirait rien — c'est
# précisément la query qui oublie le scope, seule, qu'on cherche.
missing="$(awk -v exempt="${SCOPE_EXEMPT}" '
	/^-- name:/ {
		if (name != "" && !scoped && name != exempt) print name
		name = $3
		scoped = 0
		next
	}
	name != "" && /team_id[[:space:]]*=[[:space:]]*@team_id/ { scoped = 1 }
	END {
		if (name != "" && !scoped && name != exempt) print name
	}
' "${QUERIES}")"

if [[ -n "${missing}" ]]; then
	echo "VIOLATION : query d'overview sans 'team_id = @team_id' :"
	echo "${missing}" | sed 's/^/    /'
	echo
	echo "    Ces queries lisent sans prédicat de projet. Sans le scope de team, elles lisent"
	echo "    la base entière. La seule exception admise est ${SCOPE_EXEMPT}, qui produit le scope."
	status=1
fi

# --- 2. Lecture seule ------------------------------------------------------------------------
#
# Les mots-clés sont cherchés en début d'instruction pour ne pas se déclencher sur un commentaire
# qui les mentionne — ce fichier en contient, et un garde-fou qui crie à tort finit désactivé.
writes="$(grep -nE '^[[:space:]]*(INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP)[[:space:]]' "${QUERIES}" || true)"

if [[ -n "${writes}" ]]; then
	echo "VIOLATION : ${QUERIES} doit rester en lecture seule."
	echo "${writes}" | sed 's/^/    /'
	status=1
fi

# --- 3. Aucun Overview* appelé hors de la feature --------------------------------------------
#
# internal/database/ est le code généré, il EST la query. Le reste du Go ne doit nommer Overview
# que dans internal/feature/overview/.
leaks="$(grep -rn --include='*.go' 'Overview' . \
	| grep -v '/internal/database/' \
	| grep -v "/${FEATURE_DIR}" \
	| grep -v '/scripts/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${leaks}" ]]; then
	echo "VIOLATION : une query d'overview est nommée hors de ${FEATURE_DIR}"
	echo "${leaks}" | sed 's/^/    /'
	echo
	echo "    Ces queries lisent toute une team. Les appeler depuis une autre feature contourne"
	echo "    l'isolation par projet sans qu'aucun test de tenancy ne tombe."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-overview-scope: OK"
fi

exit ${status}
