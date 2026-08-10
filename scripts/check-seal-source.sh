#!/usr/bin/env bash
# check-seal-source.sh
# The framing seal is drawn from crypto/rand, never from math/rand.
#
# WHY THIS SCRIPT EXISTS. The whole of part 1 of the trust model rests on a single property: the
# author of an issue body writes their text BEFORE the reply exists, so they cannot know the seal,
# so they cannot close the block themselves. A predictable seal makes the forged closing marker
# usable and the framing becomes decorative.
#
# WHAT NO TEST CAN DO. A black-box output test does not tell a CSPRNG from a well-seeded PRNG:
# measured, a PCG seeded on time.Now().Unix() passes go test, go vet, golangci-lint and every other
# guard — and its seed is recovered by exhaustive search over a few seconds, which makes the NEXT
# seal predictable. That is a limit of principle, not a lack of rigour in the tests. This grep is
# the only barrier available.
#
# It bounds the ACCIDENT — the refactor that swaps an import without thinking. It does not bound
# intent, and nothing can.
#
# Usage: ./scripts/check-seal-source.sh  (exit 1 on a violation)
# Used by `make lint`.

set -uo pipefail

FILE="cmd/flowlio/mcp_untrusted.go"

# The agents' throwaway worktrees are not the checkout: this script looks at one fixed path only,
# so it is immune to them by construction.

if [[ ! -f "${FILE}" ]]; then
	echo "check-seal-source: ${FILE} not found — has the file been moved?" >&2
	exit 1
fi

status=0

# The pattern avoids catching crypto/rand. Both math/rand and math/rand/v2 are refused.
if grep -nE '"math/rand(/v2)?"' "${FILE}"; then
	echo "VIOLATION: ${FILE} imports math/rand."
	echo "    The seal has to be unpredictable. math/rand is deterministic under a known seed, and"
	echo "    a seed taken from the clock is recovered by exhaustive search in seconds."
	status=1
fi

if ! grep -q '"crypto/rand"' "${FILE}"; then
	echo "VIOLATION: ${FILE} no longer imports crypto/rand."
	echo "    It is the only acceptable source of entropy for the framing seal."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-seal-source: OK"
fi
exit ${status}
