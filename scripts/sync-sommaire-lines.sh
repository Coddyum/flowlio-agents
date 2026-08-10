#!/usr/bin/env bash
# sync-sommaire-lines.sh
# Recomputes the "Ligne" column of the // SOMMAIRE blocks from the real declarations.
#
# It creates and removes no table row: when the number of rows does not match the number of
# declarations, the file is reported and left untouched — writing the description of an added
# declaration, or dropping the one that disappeared, is the author's judgement call.
#
# Usage:
#   ./scripts/sync-sommaire-lines.sh          → the whole repository
#   ./scripts/sync-sommaire-lines.sh <file>   → one file
#
# MARKER, HEADER and the "Ligne" column heading stay in French: they are compared literally against
# the text of the .go files, and the same strings are shared with flowlio-core and Flowlio. See the
# note at the top of check-sommaire.sh.

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
        return f"{path}: {len(rows)} table rows vs {len(decls)} declarations — fix it by hand"

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
        print(f"sommaire resynchronised: {path}")
    return None


problems = [msg for msg in (sync(p) for p in targets(sys.argv[1:])) if msg]
for msg in problems:
    print(msg, file=sys.stderr)
sys.exit(1 if problems else 0)
PY
