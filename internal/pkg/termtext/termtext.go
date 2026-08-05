// Package termtext is the SINGLE SINK through which all text reaching a human terminal passes.
//
// It is the terminal counterpart of what `mcp_untrusted.go` does for an agent's context: content
// written by a third party is data, never an instruction — including for a terminal emulator,
// which obeys what is written to it without ever asking who wrote it.
//
// AN ISSUE TITLE IS HOSTILE TEXT. It is written by the agent of another repo, which nobody
// reviewed, and it ends up in the terminal of a supervising human. An escape sequence does
// whatever it likes there: repainting the screen (hence lying about the state of the team),
// writing the system clipboard through OSC 52, or making the terminal WRITE to its own stdin
// through DSR — which the program reads back as keystrokes.
//
// ALLOW LIST, NEVER DENY LIST. A rune is kept if and only if `unicode.IsGraphic`. That predicate
// covers L, M, N, P, S and Zs; it therefore excludes in one move C0, C1, DEL, the Cf (Trojan
// Source bidi controls, ZWJ) and the Co. A deny list written by hand over that space — CSI, OSC,
// DCS, DSR, eight-bit C1, a bare `\r` without ESC — always ends up with a hole, and the hole only
// shows once exploited.
//
// ORDER: NEUTRALISE, THEN TRUNCATE, THEN STYLE.
//
// WHAT THAT ORDER BUYS, AND WHAT IT DOES NOT — checked by mutation, because the first draft of
// this comment got it wrong. It buys NOTHING on safety: the allow list applies anyway, so no
// active rune survives, whatever the order. What it buys is the FIDELITY OF THE DISPLAY BUDGET.
// Truncating first makes characters that are about to be removed pay for cells — and thirty
// zero-width characters at the head of a title are then enough to consume the whole column and
// push the real text out of the field. The filter stays correct; the screen is empty.
//
// The third step — styling — comes after for the opposite reason: a colour sequence WE emit must
// not be counted in the width, nor read back by the filter.
//
// WHAT THIS PACKAGE DOES NOT COVER, written down rather than killed:
//
//   - PURE HOMOGLYPHS. "СORE" with a Cyrillic С is graphic, of normal width, and visually
//     identical to CORE. The only defence would be an allow list of Unicode scripts, which we
//     refuse to impose on issue titles written in French.
//
//   - THE TEXTUAL RESIDUE OF A SEQUENCE. `\x1b[2J` loses its ESC and leaves "[2J" on screen. That
//     residue is INERT — without its introducer, the terminal displays it like any other text —
//     but it is visible, and a long enough payload can consume the width of a column and push the
//     real title out of the field. It is an attack on LEGIBILITY, never on control of the
//     terminal. Removing it would require recognising sequence shapes, that is to say a deny list
//     — which this package refuses to be, and which would eat a bug report about escape sequences
//     along the way.
//
// Both are known risks, not covered, and accepted as such.
package termtext

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                                | Ligne |
// |----------|-----------------------------------------------------------------------|-------|
// | Line     | Neutralises a one-line field and bounds it in display cells             | 80    |
// | Block    | Neutralises a multi-line body, keeping its line breaks                  | 115   |
// | Cells    | Measures the DISPLAY width, not the number of runes                     | 139   |
// | runeCells| Display width of a single rune                                          | 155   |
// | truncate | Cuts at n cells, laying an ellipsis when text is left over              | 175   |
//
// Fin du sommaire.
// =====================================================================

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// ellipsis marks a truncation. One character, one cell.
const ellipsis = '…'

// Line neutralises a field meant to fit on ONE line — title, key, author name — and bounds it to
// cells display cells.
//
// Line breaks are removed like everything else: a `\n` inside a title inserts a fake row into a
// table, which shifts everything that follows and fabricates a line whose content nobody wrote.
//
// cells <= 0 means "no bound": the text is neutralised, not truncated. That is the case of a field
// whose column adapts, never that of a field somebody forgot to bound — the two are told apart by
// reading the caller.
func Line(s string, cells int) string {
	var b strings.Builder
	b.Grow(len(s))

	// Neutralise FIRST: truncating before would filter a sequence cut in two, the remaining half
	// of which can stay active.
	for _, r := range s {
		if !unicode.IsGraphic(r) {
			continue
		}
		b.WriteRune(r)
	}

	// Edge spaces are removed AFTER the filter: a removed sequence leaves behind spaces that came
	// from nothing.
	clean := strings.TrimSpace(b.String())
	if cells <= 0 {
		return clean
	}
	return truncate(clean, cells)
}

// Block neutralises a multi-line body.
//
// Two exceptions to the allow list, and two only:
//
//   - `\n` is KEPT. A message body is structured by its lines; removing them would make illegible
//     what this package exists to make legible.
//   - `\t` becomes TWO SPACES. A tabulation moves the cursor to a stop whose position depends on
//     the terminal: it cannot repaint the screen, but it breaks any alignment computed in cells.
//     Converting it keeps the intent (the indentation) without the effect.
//
// The `\r` is NOT kept, and that is the case that counts: "all is well\rALERT" contains no ESC at
// all, so it crosses any filter that only looks for `0x1b`, and it rewrites the line from its
// start.
func Block(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("  ")
		case unicode.IsGraphic(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Cells measures the DISPLAY width of a string, in terminal columns.
//
// Counting runes would give a wrong alignment as soon as a title contains CJK or an emoji — two
// columns each — or a combining mark, which occupies none. A table whose columns shift is a table
// people stop reading.
//
// The string is assumed to be neutralised already: Cells does not filter, it measures.
func Cells(s string) int {
	total := 0
	for _, r := range s {
		total += runeCells(r)
	}
	return total
}

// runeCells yields the display width of a rune: 0, 1 or 2.
//
// Combining marks (Mn, Me) sit ON the preceding character and occupy no column — counting them
// would shift every line containing a decomposed accent.
//
// `width.LookupRune` tells apart the East Asian Wide and Fullwidth forms, which are worth two
// columns. Ambiguous is worth 1: its width depends on the terminal locale, and assuming 2 would
// break the alignment of the common case to protect a rare one.
func runeCells(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

// truncate cuts an ALREADY NEUTRALISED string at n display cells.
//
// When text is left over, the last cell carries an ellipsis: without it, a cut title reads like a
// complete title, and the human believes they read everything. The ellipsis is part of the budget,
// it is not added to it.
//
// A two-cell rune that does not fit in the remaining room is dropped entirely: cutting it in half
// makes no sense, a rune has no half.
func truncate(s string, n int) string {
	if Cells(s) <= n {
		return s
	}
	if n <= 1 {
		return string(ellipsis)
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeCells(r)
		if used+w > n-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteRune(ellipsis)
	return b.String()
}
