#!/usr/bin/env bash
# check-overview-scope.sh
# overview is the only place in the repository that reads business rows without a project predicate.
#
# WHY THIS SCRIPT EXISTS. Everywhere else, a query that forgets its scope is caught by the fact that
# the project comes from the token. Here the rule is the OPPOSITE — team_id alone — so there is no
# implicit guard left. Three things can go wrong, and none of them shows up when reading a diff:
#
#   1. an overview query that forgets `team_id = @team_id` reads the WHOLE database, every team
#      together;
#   2. an INSERT/UPDATE/DELETE slipping in makes a read-only surface mutable, behind a route no
#      write test covers;
#   3. and above all: `internal/database` is importable from anywhere. A contributor who finds
#      `OverviewIssueDebts` handy and calls it from `issue/store` gets a complete leak — and
#      check-cross-feature-imports.sh sees NOTHING, because `internal/database` is not a feature.
#
# The third one is the real standing risk. This script does not make it zero, it makes it loud.
#
# Usage: ./scripts/check-overview-scope.sh  (exit 1 on a violation)
# Used by `make lint`.

set -uo pipefail

QUERIES="sql/queries/overview.sql"
FEATURE_DIR="internal/feature/overview/"

# OverviewTeamBySlug is the NAMED exception: it PRODUCES the scope out of a slug, so it cannot carry
# it. Any other exception added here is a security decision.
SCOPE_EXEMPT="OverviewTeamBySlug"

status=0

if [[ ! -f "${QUERIES}" ]]; then
	echo "check-overview-scope: ${QUERIES} not found"
	exit 1
fi

# --- 1. Every `-- name:` block carries team_id = @team_id ------------------------------------
#
# The split happens on the `-- name:` lines: a query's body is everything following it up to the
# next one. Counting occurrences over the whole file would say nothing — what is being looked for is
# precisely the one query, on its own, that forgot the scope.
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
	echo "VIOLATION: overview query without 'team_id = @team_id':"
	echo "${missing}" | sed 's/^/    /'
	echo
	echo "    These queries read with no project predicate. Without the team scope they read the"
	echo "    entire database. The only admitted exception is ${SCOPE_EXEMPT}, which produces the"
	echo "    scope."
	status=1
fi

# --- 2. Read-only ----------------------------------------------------------------------------
#
# The keywords are looked for at the start of a statement so as not to fire on a comment mentioning
# them — this file contains some, and a guard that cries wolf ends up switched off.
writes="$(grep -nE '^[[:space:]]*(INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP)[[:space:]]' "${QUERIES}" || true)"

if [[ -n "${writes}" ]]; then
	echo "VIOLATION: ${QUERIES} has to stay read-only."
	echo "${writes}" | sed 's/^/    /'
	status=1
fi

# --- 3. No Overview* called outside the feature ----------------------------------------------
#
# internal/database/ is the generated code, it IS the query. The rest of the Go may name Overview
# only inside internal/feature/overview/.
leaks="$(grep -rn --include='*.go' 'Overview' . \
	| grep -v '/internal/database/' \
	| grep -v "/${FEATURE_DIR}" \
	| grep -v '/scripts/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${leaks}" ]]; then
	echo "VIOLATION: an overview query is named outside ${FEATURE_DIR}"
	echo "${leaks}" | sed 's/^/    /'
	echo
	echo "    These queries read a whole team. Calling them from another feature bypasses the"
	echo "    per-project isolation without a single tenancy test failing."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-overview-scope: OK"
fi

exit ${status}
