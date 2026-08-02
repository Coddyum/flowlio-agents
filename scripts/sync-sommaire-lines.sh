#!/usr/bin/env bash
# sync-sommaire-lines.sh
# Recalcule la colonne "Ligne" des blocs // SOMMAIRE à partir des déclarations réelles.
#
# Ne crée ni ne supprime aucune ligne de tableau : si le nombre de lignes ne correspond pas au
# nombre de déclarations, le fichier est signalé et laissé intact — c'est à l'auteur d'écrire la
# description de la déclaration ajoutée ou de retirer celle qui a disparu.
#
# Usage :
#   ./scripts/sync-sommaire-lines.sh            → tout le repo
#   ./scripts/sync-sommaire-lines.sh <fichier>  → un seul fichier

set -euo pipefail

python3 - "$@" <<'PY'
import re
import sys
from pathlib import Path

MARKER = "// SOMMAIRE (lire en premier, sauter directement au bon passage)"
DECL = re.compile(r"^(func |type )")
ROW = re.compile(r"^// \|")
HEADER = re.compile(r"^// \| *Élément")
SEPARATOR = re.compile(r"^// \|[-| ]+\|?\s*$")

EXCLUDED_PARTS = ("internal/database", ".git", "blueprint")


def targets(argv):
    if argv:
        return [Path(a) for a in argv]
    return [
        p
        for p in Path(".").rglob("*.go")
        if not any(part in str(p) for part in EXCLUDED_PARTS)
        and not p.name.endswith("_test.go")
    ]


def sync(path):
    lines = path.read_text().splitlines()
    if not any(line.startswith(MARKER) for line in lines):
        return None

    rows = [
        i
        for i, line in enumerate(lines)
        if ROW.match(line) and not HEADER.match(line) and not SEPARATOR.match(line)
    ]
    decls = [i + 1 for i, line in enumerate(lines) if DECL.match(line)]

    if len(rows) != len(decls):
        return f"{path}: {len(rows)} lignes de tableau vs {len(decls)} déclarations — à corriger à la main"

    changed = False
    for row_index, decl_line in zip(rows, decls):
        cells = lines[row_index].split("|")
        if len(cells) < 4:
            continue
        width = len(cells[-2])
        replacement = f" {decl_line}".ljust(width)
        if cells[-2] != replacement:
            cells[-2] = replacement
            lines[row_index] = "|".join(cells)
            changed = True

    if changed:
        path.write_text("\n".join(lines) + "\n")
        print(f"sommaire resynchronisé : {path}")
    return None


problems = [msg for msg in (sync(p) for p in targets(sys.argv[1:])) if msg]
for msg in problems:
    print(msg, file=sys.stderr)
sys.exit(1 if problems else 0)
PY
