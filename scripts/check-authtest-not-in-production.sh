#!/usr/bin/env bash
# check-authtest-not-in-production.sh
# Le harnais d'authentification n'entre jamais dans le binaire.
#
# `internal/core/auth/authtest` expose un auth.Store factice dont un test choisit la portée. C'est
# exactement ce qu'il faut à un test de route, et exactement ce qu'il ne faut nulle part ailleurs :
# importé depuis un fichier de production, il donnerait un chemin d'authentification qui ne
# consulte pas la base.
#
# Un paquet `*test` n'est pas protégé par le compilateur — seuls les fichiers `_test.go` d'un même
# paquet le sont. Ce grep est la seule barrière.
#
# Usage : ./scripts/check-authtest-not-in-production.sh  (exit 1 si violation)
# Utilisé par `make lint`.

set -uo pipefail

hits="$(grep -rln --include='*.go' 'core/auth/authtest' . \
	| grep -v '_test\.go$' \
	| grep -v '/authtest/' \
	| grep -v '^\./\.claude/' || true)"

if [[ -n "${hits}" ]]; then
	echo "VIOLATION : authtest est importé depuis du code de production."
	echo "${hits}" | sed 's/^/    /'
	echo
	echo "    Ce paquet fabrique un auth.Store qui ne consulte aucune base. Il n'a de sens que"
	echo "    dans un _test.go."
	exit 1
fi

echo "check-authtest-not-in-production: OK"
