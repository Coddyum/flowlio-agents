#!/usr/bin/env bash
# check-trust-in-sql-only.sh
# The trust decision lives in a SQL query, never in Go.
#
# WHY THIS SCRIPT EXISTS. The trust graph authorises the cross-project channel through a single
# EXISTS in CreateIssue's WHERE (sql/queries/issues.sql). That placement is not a matter of taste:
# measured, moving it consumes the recipient's counter and takes a lock on its row, which hands an
# unauthorised sender a write channel into their victim AND a denial of service aimed at a
# legitimate third party. If a service, a handler or a store needs to NAME the table, the decision
# has left the query — and no amount of review catches that reliably.
#
# WHAT IS TOLERATED, AND WHY:
#   - internal/database/  : sqlc-generated code, which IS the query.
#   - *_test.go           : a fixture is not a decision. The integration tests lay the graph out by
#                           hand, deliberately — hiding it in a project-creation helper would mask
#                           the very guarantee those tests exist to prove.
#
# Usage: ./scripts/check-trust-in-sql-only.sh  (exit 1 on a violation)
# Used by `make lint`.

set -uo pipefail

TABLE="project_trust"

# grep -r returns 1 when it finds nothing, which is the nominal case here: `|| true` keeps `set -e`
# from failing the script on a success.
hits="$(grep -rn --include='*.go' "${TABLE}" . \
	| grep -v '/internal/database/' \
	| grep -v '_test\.go:' \
	| grep -v '/scripts/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${hits}" ]]; then
	echo "VIOLATION: the ${TABLE} table is named in non-generated, non-test Go."
	echo "${hits}" | sed 's/^/    /'
	echo
	echo "    The trust decision is a PREDICATE in sql/queries/issues.sql (CreateIssue), and its"
	echo "    administration lives in sql/queries/trust.sql. A .go file naming the table has taken"
	echo "    the decision out of the query — which reopens the channel this closes."
	exit 1
fi

echo "check-trust-in-sql-only: OK"
