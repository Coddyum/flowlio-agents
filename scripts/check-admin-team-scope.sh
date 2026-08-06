#!/usr/bin/env bash
# check-admin-team-scope.sh
# Toute route montée derrière AdminOnly doit BORNER une team.
#
# POURQUOI CE SCRIPT EXISTE. `AdminOnly` prouve une PORTÉE de token, jamais une PORTÉE DE REQUÊTE.
# Il répond à « ce token est-il d'administration ? » et rien d'autre : la question « et sur quelle
# team ? » n'est posée nulle part dans le middleware. Chaque handler admin doit donc la poser
# lui-même, et rien dans le langage ne l'y oblige.
#
# C'est le point 1 de FLWL-70. Deux routes l'avaient oublié — `GET /teams` énumérait toute
# l'installation, `POST /teams` ne lisait même pas son principal — et rien ne l'avait signalé :
# elles compilaient, passaient `AdminOnly`, et les tests d'isolation des features restaient verts
# parce qu'ils vérifient que les queries de `task` et `issue` sont scopées, pas qu'une route admin
# borne quoi que ce soit.
#
# Ce que le script vérifie, et c'est tout ce qu'il peut vérifier mécaniquement : le corps du
# handler NOMME la team du principal, d'une des deux façons admises —
#
#   * `teamFor(...)`      — il résout un `?team=<slug>` et le confronte au principal (le cas
#                           général : huit routes sur dix) ;
#   * `principal.TeamID`  — il décide lui-même, et le commentaire au-dessus dit pourquoi (les deux
#                           routes `/teams`, qui ne résolvent aucun slug).
#
# Il ne prouve pas que la borne est JUSTE — ça, c'est le rôle des tests de mutation de
# `workspace/handler/handler_test.go`. Il rend bruyant le fait de n'en avoir mis aucune.
#
# Usage : ./scripts/check-admin-team-scope.sh  (exit 1 si violation)
# Utilisé par `make lint`.

set -uo pipefail

status=0
found_any=0

# --- 1. Inventaire des handlers montés derrière `admin(...)` ---------------------------------
#
# Le motif suivi est celui, unique dans le dépôt, de `Routes()` : `admin` est l'alias local de
# `m.auth.AdminOnly`, lié une fois en tête de la méthode (règle de module-system.md). Un module qui
# écrirait `m.auth.AdminOnly(...)` en toutes lettres dans la table de routes échapperait à ce
# grep — la seconde passe, plus bas, est là pour ça.
for module_file in internal/feature/*/module.go; do
	feature_dir="$(dirname "${module_file}")"
	handler_dir="${feature_dir}/handler"

	[[ -d "${handler_dir}" ]] || continue

	handlers="$(grep -oE 'admin\(http\.HandlerFunc\(m\.h\.[A-Za-z0-9_]+\)\)' "${module_file}" \
		| sed -E 's/.*m\.h\.([A-Za-z0-9_]+)\)\)/\1/' | sort -u)"

	[[ -n "${handlers}" ]] || continue

	while IFS= read -r name; do
		found_any=1

		# Le corps du handler : de sa signature jusqu'à l'accolade fermante en colonne 0.
		body="$(awk -v want="func (h *Handler) ${name}(" '
			index($0, want) == 1 { inside = 1 }
			inside { print }
			inside && /^}/ { exit }
		' "${handler_dir}"/*.go)"

		if [[ -z "${body}" ]]; then
			echo "VIOLATION : handler admin ${name} introuvable dans ${handler_dir}/"
			echo "    Monté derrière admin(...) dans ${module_file}, mais aucun"
			echo "    'func (h *Handler) ${name}(' ne lui correspond. Le grep de ce script a"
			echo "    dérivé, ou la route pointe ailleurs — les deux se corrigent ici."
			status=1
			continue
		fi

		if grep -qE 'teamFor\(|principal\.TeamID|p\.TeamID' <<<"${body}"; then
			continue
		fi

		echo "VIOLATION : ${feature_dir#internal/feature/} — ${name} ne borne aucune team."
		echo "    Monté derrière AdminOnly dans ${module_file}, il ne nomme ni teamFor(),"
		echo "    ni la team du principal. AdminOnly prouve la portée du TOKEN, pas celle de la"
		echo "    REQUÊTE : en l'état, un admin épinglé à une team agit hors de la sienne, et le"
		echo "    jour où le troisième scope existe, personne ne repassera par ici."
		status=1
	done <<<"${handlers}"
done

# --- 2. Aucun AdminOnly écrit en toutes lettres dans une table de routes ----------------------
#
# La première passe ne voit que l'alias `admin(...)`. Une route écrite `m.auth.AdminOnly(...)`
# directement dans `Routes()` passerait donc sous le radar en gardant l'air correcte. On refuse la
# forme plutôt que d'essayer de la suivre : la liaison unique du middleware est déjà une règle du
# dépôt (module-system.md).
inline="$(grep -n 'r\.Handle(.*m\.auth\.AdminOnly' internal/feature/*/module.go || true)"

if [[ -n "${inline}" ]]; then
	echo "VIOLATION : AdminOnly appelé directement dans une table de routes."
	echo "${inline}" | sed 's/^/    /'
	echo
	echo "    Lier le middleware une fois en tête de Routes() — 'admin := m.auth.AdminOnly' —"
	echo "    puis l'utiliser. Sans cet alias, l'inventaire ci-dessus ne voit pas la route."
	status=1
fi

# --- 3. L'inventaire n'est pas vide ----------------------------------------------------------
#
# Un grep qui ne matche plus rien reste vert, et c'est la panne la plus coûteuse d'un garde-fou :
# il continue de s'afficher OK en ne gardant plus rien. Renommer l'alias `admin` suffirait à le
# provoquer.
if [[ ${found_any} -eq 0 ]]; then
	echo "VIOLATION : aucune route admin trouvée dans internal/feature/*/module.go."
	echo "    Le motif suivi par ce script ne matche plus rien. Il ne garde donc plus rien,"
	echo "    tout en restant vert. Corriger le motif, pas ce message."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-admin-team-scope: OK"
fi

exit ${status}
