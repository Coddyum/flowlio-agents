#!/usr/bin/env bash
# hook-go-postedit.sh
# Hook PostToolUse (Edit|Write). Lit le JSON du hook sur stdin, extrait le fichier édité.
# Si .go : go build + go vet + garde-fous structurels. Exit 2 = bloque (feedback à Claude).
#
# Câblage dans .claude/settings.json (voir bundle). Ne rien faire si l'outil n'a pas touché de .go.

set -uo pipefail

cd "$(dirname "$0")/.."

payload="$(cat)"

# Extraire le chemin du fichier depuis le payload du hook (tool_input.file_path).
file="$(printf '%s' "$payload" | sed -nE 's/.*"file_path"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -n1)"

[[ -n "$file" && "$file" == *.go ]] || exit 0

# An edit outside this repository is not ours to judge. Without this the hook fires on sibling
# repositories whenever the session's working directory is elsewhere: it then runs `go build`
# on THIS module and demands a sommaire on THEIR file. Observed live on 2026-08-05, blocking an
# edit to a flowlio-core file from a session rooted here.
repo_root="$(pwd)"
rel="${file#"$repo_root"/}"
[[ "$rel" != /* ]] || exit 0

fail=0

# 1. Compilation + vet sur tout le module (une erreur ailleurs doit aussi bloquer).
if ! go build ./... 2>&1; then
	echo "hook: go build échoue — corriger avant de continuer." >&2
	fail=2
fi
if ! go vet ./... 2>&1; then
	echo "hook: go vet échoue." >&2
	fail=2
fi

# 2. Imports inter-features (si le fichier est sous une feature).
if [[ "$file" == *internal/feature/* ]]; then
	if ! ./scripts/check-cross-feature-imports.sh >&2; then
		fail=2
	fi
fi

# 3. Sommaire du fichier édité : les numéros de ligne sont resynchronisés automatiquement,
#    seule une ligne de tableau manquante ou en trop reste à corriger à la main.
./scripts/sync-sommaire-lines.sh "$file" >&2 || true
if ! ./scripts/check-sommaire.sh "$file" >&2; then
	fail=2
fi

exit $fail
