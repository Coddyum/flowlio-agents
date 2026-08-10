#!/usr/bin/env bash
# check-authtest-not-in-production.sh
# The authentication harness never enters the binary.
#
# `internal/core/auth/authtest` exposes a fake auth.Store whose scope a test chooses. That is
# exactly what a route test needs, and exactly what nothing else may have: imported from a
# production file, it would give an authentication path that never consults the database.
#
# A `*test` package is NOT protected by the compiler — only the `_test.go` files of one package are.
# This grep is the only barrier.
#
# Usage: ./scripts/check-authtest-not-in-production.sh  (exit 1 on a violation)
# Used by `make lint`.

set -uo pipefail

hits="$(grep -rln --include='*.go' 'core/auth/authtest' . \
	| grep -v '_test\.go$' \
	| grep -v '/authtest/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${hits}" ]]; then
	echo "VIOLATION: authtest is imported from production code."
	echo "${hits}" | sed 's/^/    /'
	echo
	echo "    This package builds an auth.Store that consults no database. It only makes sense"
	echo "    inside a _test.go."
	exit 1
fi

echo "check-authtest-not-in-production: OK"
