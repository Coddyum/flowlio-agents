#!/usr/bin/env bash
# check-admin-team-scope.sh
# Every route mounted behind AdminOnly has to BOUND a team.
#
# WHY THIS SCRIPT EXISTS. `AdminOnly` proves a TOKEN's scope, never a REQUEST's. It answers "is this
# an administration token?" and nothing else: the question "and on which team?" is asked nowhere in
# the middleware. Each admin handler therefore has to ask it itself, and nothing in the language
# forces it to.
#
# This is point 1 of FLWL-70. Two routes had forgotten it — `GET /teams` enumerated the whole
# installation, `POST /teams` did not even read its principal — and nothing had flagged it: they
# compiled, they passed `AdminOnly`, and the features' isolation tests stayed green because they
# check that the `task` and `issue` queries are scoped, not that an admin route bounds anything.
#
# What the script checks, and it is all it can check mechanically: the handler's body NAMES the
# principal's team, in one of the two admitted ways —
#
#   * `teamFor(...)`      — it resolves a `?team=<slug>` and confronts it with the principal (the
#                           general case: eight routes out of ten);
#   * `principal.TeamID`  — it decides for itself, and the comment above it says why (the two
#                           `/teams` routes, which resolve no slug).
#
# It does not prove the bound is RIGHT — that is the job of the mutation tests in
# `workspace/handler/handler_test.go`. It makes having put none of them loud.
#
# Usage: ./scripts/check-admin-team-scope.sh  (exit 1 on a violation)
# Used by `make lint`.

set -uo pipefail

status=0
found_any=0

# --- 1. Inventory of the handlers mounted behind `admin(...)` --------------------------------
#
# The pattern followed is the one, unique in this repository, of `Routes()`: `admin` is the local
# alias of `m.auth.AdminOnly`, bound once at the top of the method (the rule in module-system.md). A
# module writing `m.auth.AdminOnly(...)` out in full in its route table would escape this grep — the
# second pass, below, exists for that.
for module_file in internal/feature/*/module.go; do
	feature_dir="$(dirname "${module_file}")"
	handler_dir="${feature_dir}/handler"

	[[ -d "${handler_dir}" ]] || continue

	handlers="$(grep -oE 'admin\(http\.HandlerFunc\(m\.h\.[A-Za-z0-9_]+\)\)' "${module_file}" \
		| sed -E 's/.*m\.h\.([A-Za-z0-9_]+)\)\)/\1/' | sort -u)"

	[[ -n "${handlers}" ]] || continue

	while IFS= read -r name; do
		found_any=1

		# The handler's body: from its signature to the closing brace in column 0.
		body="$(awk -v want="func (h *Handler) ${name}(" '
			index($0, want) == 1 { inside = 1 }
			inside { print }
			inside && /^}/ { exit }
		' "${handler_dir}"/*.go)"

		if [[ -z "${body}" ]]; then
			echo "VIOLATION: admin handler ${name} not found in ${handler_dir}/"
			echo "    Mounted behind admin(...) in ${module_file}, but no matching"
			echo "    'func (h *Handler) ${name}(' exists. Either this script's grep has drifted,"
			echo "    or the route points elsewhere — both are fixed here."
			status=1
			continue
		fi

		if grep -qE 'teamFor\(|principal\.TeamID|p\.TeamID' <<<"${body}"; then
			continue
		fi

		echo "VIOLATION: ${feature_dir#internal/feature/} — ${name} bounds no team."
		echo "    Mounted behind AdminOnly in ${module_file}, it names neither teamFor() nor the"
		echo "    principal's team. AdminOnly proves the scope of the TOKEN, not of the REQUEST:"
		echo "    as it stands, an admin pinned to one team acts outside their own, and the day a"
		echo "    third scope exists, nobody will come back through here."
		status=1
	done <<<"${handlers}"
done

# --- 2. No AdminOnly written out in full in a route table ------------------------------------
#
# The first pass only sees the `admin(...)` alias. A route written `m.auth.AdminOnly(...)` straight
# into `Routes()` would slip under the radar while looking correct. The form is refused rather than
# followed: binding the middleware once is already a rule of this repository (module-system.md).
inline="$(grep -n 'r\.Handle(.*m\.auth\.AdminOnly' internal/feature/*/module.go || true)"

if [[ -n "${inline}" ]]; then
	echo "VIOLATION: AdminOnly called directly inside a route table."
	echo "${inline}" | sed 's/^/    /'
	echo
	echo "    Bind the middleware once at the top of Routes() — 'admin := m.auth.AdminOnly' — then"
	echo "    use it. Without that alias, the inventory above does not see the route."
	status=1
fi

# --- 3. The inventory is not empty -----------------------------------------------------------
#
# A grep that no longer matches anything stays green, and that is the most expensive way for a guard
# to break: it goes on reporting OK while guarding nothing. Renaming the `admin` alias would be
# enough to cause it.
if [[ ${found_any} -eq 0 ]]; then
	echo "VIOLATION: no admin route found in internal/feature/*/module.go."
	echo "    The pattern this script follows no longer matches anything. It therefore guards"
	echo "    nothing while staying green. Fix the pattern, not this message."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-admin-team-scope: OK"
fi

exit ${status}
