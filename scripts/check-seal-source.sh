#!/usr/bin/env bash
# check-seal-source.sh
# Le sceau de balisage est tiré par crypto/rand, jamais par math/rand.
#
# POURQUOI CE SCRIPT EXISTE. Tout le volet 1 du modèle de confiance repose sur une seule
# propriété : l'auteur d'un corps d'issue écrit son texte AVANT que la réponse existe, donc il ne
# peut pas connaître le sceau, donc il ne peut pas refermer son bloc. Un sceau prévisible rend la
# fausse balise fermante exploitable et le balisage devient décoratif.
#
# CE QU'AUCUN TEST NE PEUT FAIRE. Un test de sortie en boîte noire ne distingue pas un CSPRNG d'un
# PRNG bien amorcé : mesuré, un PCG amorcé sur time.Now().Unix() passe go test, go vet,
# golangci-lint et les autres garde-fous — et sa graine se retrouve par recherche exhaustive sur
# quelques secondes, ce qui rend le sceau SUIVANT prédictible. C'est une limite de principe, pas
# un manque de rigueur des tests. Ce grep est la seule barrière possible.
#
# Il borne l'ACCIDENT — le refactor qui remplace un import sans y penser. Il ne borne pas
# l'intention, et rien ne le peut.
#
# Usage : ./scripts/check-seal-source.sh  (exit 1 si violation)
# Utilisé par `make lint`.

set -uo pipefail

FILE="cmd/flowlio/mcp_untrusted.go"

if [[ ! -f "${FILE}" ]]; then
	echo "check-seal-source: ${FILE} introuvable — le fichier a-t-il été déplacé ?" >&2
	exit 1
fi

status=0

# Le \b évite d'accrocher crypto/rand. math/rand comme math/rand/v2 sont refusés.
if grep -nE '"math/rand(/v2)?"' "${FILE}"; then
	echo "VIOLATION : ${FILE} importe math/rand."
	echo "    Le sceau doit être imprévisible. math/rand est déterministe à graine connue, et une"
	echo "    graine tirée de l'horloge se retrouve par recherche exhaustive en quelques secondes."
	status=1
fi

if ! grep -q '"crypto/rand"' "${FILE}"; then
	echo "VIOLATION : ${FILE} n'importe plus crypto/rand."
	echo "    C'est la seule source d'entropie acceptable pour le sceau de balisage."
	status=1
fi

if [[ ${status} -eq 0 ]]; then
	echo "check-seal-source: OK"
fi
exit ${status}
