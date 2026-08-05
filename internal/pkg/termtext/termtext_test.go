package termtext

// What this file locks down: every family of hostile sequence is neutralised, and the order
// neutralise → truncate is respected.
//
// This is NOT the real control of the product. The real control is the test that will prove no
// view forgets to call this package — a perfect filter that a single rendering line bypasses
// protects nothing. These tests guarantee the filter does what it claims; the coverage guardrail
// will come with the first renderer.

import (
	"strings"
	"testing"
	"unicode"
)

// family describes a hostile payload and what must not survive it.
type family struct {
	name string
	// payload is the text as an agent of another repo could write it into a title.
	payload string
	// forbidden lists what must NOT come back out. CONTROL CHARACTERS go there, never the textual
	// residue of a sequence: deprived of its ESC introducer, "[2J" is nothing but inert text the
	// terminal displays without interpreting. Requiring it to disappear would take a deny list —
	// which this package refuses to be — and would eat a bug report talking about escape
	// sequences.
	forbidden []string
	// keeps lists what must come back out: we frame the risk, we do not mutilate the text.
	keeps string
}

var families = []family{
	{
		name:      "CSI — repainting the screen, hence lying about the state of the team",
		payload:   "\x1b[2J\x1b[Hall is well",
		forbidden: []string{"\x1b"},
		keeps:     "all is well",
	},
	{
		name:      "OSC 52 — writes the system clipboard",
		payload:   "login\x1b]52;c;ZXhmaWx0cmF0aW9u\x07 outage",
		forbidden: []string{"\x1b", "\x07"},
		keeps:     "login",
	},
	{
		name:      "OSC 8 — clickable hyperlink to an address nobody wrote",
		payload:   "\x1b]8;;http://evil.example\x1b\\click here\x1b]8;;\x1b\\",
		forbidden: []string{"\x1b", "\x1b\\"},
		keeps:     "click here",
	},
	{
		name: "DSR — makes the terminal WRITE to its own stdin, which the TUI reads back as keys",
		// The only family of the lot that goes all the way to execution.
		payload:   "status?\x1b[6n",
		forbidden: []string{"\x1b"},
		keeps:     "status?",
	},
	{
		name: "bare C0 — NO ESC at all, hence invisible to any filter that only looks for 0x1b",
		// The carriage return rewrites the line from its start: the human never reads "all is
		// well", only "ALERT".
		payload:   "all is well\rALERT",
		forbidden: []string{"\r"},
		keeps:     "ALERT",
	},
	{
		name:      "eight-bit C1 — single-byte CSI introducer",
		payload:   "before\u009b2Kafter",
		forbidden: []string{"\u009b"},
		keeps:     "after",
	},
	{
		name:      "line break inside a title — inserts a fake row into the table",
		payload:   "real line\nfabricated line",
		forbidden: []string{"\n"},
		keeps:     "fabricated line",
	},
	{
		name: "bidi controls — Trojan Source: what is displayed is not what the title contains",
		// U+202E RIGHT-TO-LEFT OVERRIDE.
		payload:   "fix \u202esaisiaton ni slaitnederc eht",
		forbidden: []string{"\u202e"},
		keeps:     "fix",
	},
	{
		name:      "zero width — visually separates nothing, breaks a key comparison",
		payload:   "CO\u200bRE",
		forbidden: []string{"\u200b"},
		keeps:     "CORE",
	},
	{
		name:      "DEL",
		payload:   "before\x7fafter",
		forbidden: []string{"\x7f"},
		keeps:     "afte",
	},
}

// Every family is neutralised, and what was legitimate survives.
//
// MUTATIONS: filtering only 0x1b → the "bare C0" line goes red. Ignoring the C1 → the C1 line goes
// red. Keeping the Cf → the bidi line goes red.
func TestLineNeutralisesHostileText(t *testing.T) {
	for _, f := range families {
		t.Run(f.name, func(t *testing.T) {
			got := Line(f.payload, 0)

			for _, bad := range f.forbidden {
				if strings.Contains(got, bad) {
					t.Errorf("the sequence %q survives in %q", bad, got)
				}
			}
			if f.keeps != "" && !strings.Contains(got, f.keeps) {
				t.Errorf("the legitimate text %q disappeared from %q — we frame the risk, "+
					"we do not mutilate the content", f.keeps, got)
			}
			// Generic control: NO non-graphic rune left, whatever the family.
			for _, r := range got {
				if !unicode.IsGraphic(r) {
					t.Errorf("non-graphic rune U+%04X survives in %q", r, got)
				}
			}
		})
	}
}

// Block keeps the line breaks, converts the tabulations, and removes everything else.
func TestBlockKeepsStructureAndNothingElse(t *testing.T) {
	got := Block("line 1\n\tindented\rERASES\x1b[2Jrest\n")

	if strings.Count(got, "\n") != 2 {
		t.Errorf("%d line breaks, expected 2: a body is structured by its lines\n%q",
			strings.Count(got, "\n"), got)
	}
	if !strings.Contains(got, "  indented") {
		t.Errorf("the tabulation did not become two spaces: %q", got)
	}
	if strings.ContainsAny(got, "\r\x1b\t") {
		t.Errorf("a control survives: %q", got)
	}
	if !strings.Contains(got, "ERASES") || !strings.Contains(got, "rest") {
		t.Errorf("legitimate content disappeared: %q", got)
	}
}

// THE WIDTH IS MEASURED IN DISPLAY CELLS, NOT IN RUNES.
//
// Counting runes shifts every line containing CJK, an emoji or a decomposed accent — and a table
// whose columns move is a table people stop reading.
//
// MUTATION: yielding 1 for every rune in runeCells → the CJK and emoji cases go red.
func TestCellsMeasuresDisplayWidth(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"ascii", "CORE", 4},
		{"precomposed accent", "é", 1},
		// Written as an escape and not as a literal: an editor or a formatter normalising the file
		// would silently turn it back into the precomposed form, and this case would stop testing
		// anything while staying green.
		{"decomposed accent — the combining mark occupies no column", "e\u0301", 1},
		{"CJK — two columns per ideogram", "日本語", 6},
		{"fullwidth", "ＣＯＲＥ", 8},
		{"mixed", "bug 日本", 8},
		{"empty", "", 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Cells(c.text); got != c.want {
				t.Errorf("Cells(%q) = %d, expected %d", c.text, got, c.want)
			}
		})
	}
}

// THE ORDER IS NEUTRALISE THEN TRUNCATE, and this test says exactly what it proves.
//
// IT DOES NOT PROVE A SAFETY PROPERTY. The allow list applies in both orders, so no active rune
// survives anyway — the first draft of this file claimed otherwise, and the mutation disproved it.
//
// What it proves is the FIDELITY OF THE BUDGET: under the reverse order, characters meant to
// disappear consume cells. Thirty ZERO-width characters at the head of a title are enough to eat
// the whole column and push the real text out of the field. The screen is then empty, and nothing
// failed.
//
// MUTATION: truncating before filtering in Line → this test goes red, the output holds nothing but
// the ellipsis.
func TestNeutralisationHappensBeforeTruncation(t *testing.T) {
	// U+200B ZERO WIDTH SPACE: non-graphic, hence removed — but counted while it is still there.
	payload := strings.Repeat("\u200b", 30) + "readable title"

	got := Line(payload, 20)

	if !strings.Contains(got, "readable title") {
		t.Errorf("got = %q — thirty invisible characters consumed the column: the truncation "+
			"happened BEFORE the filter", got)
	}
}

// The bound is in CELLS, and the ellipsis is part of the budget — not added after it.
func TestLineTruncatesToCellBudget(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		cells int
		want  string
	}{
		{"fits as is", "CORE", 10, "CORE"},
		{"exactly at the bound", "CORE", 4, "CORE"},
		{"cut", "a title that is far too long", 10, "a title t…"},
		{"bound at 1", "title", 1, "…"},
		{"zero bound = no bound", "a title that is far too long", 0, "a title that is far too long"},
		// A two-cell rune that does not fit in the remaining room is dropped WHOLE: a rune has no
		// half.
		{"CJK cut on an odd boundary", "日本語", 4, "日…"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Line(c.text, c.cells)
			if got != c.want {
				t.Errorf("Line(%q, %d) = %q, expected %q", c.text, c.cells, got, c.want)
			}
			if c.cells > 0 && Cells(got) > c.cells {
				t.Errorf("the output is %d cells, above the bound %d: the ellipsis was added to "+
					"the budget instead of being part of it", Cells(got), c.cells)
			}
		})
	}
}

// A truncation must SHOW. Without an ellipsis, a cut title reads like a complete title, and the
// human believes they read everything — which is worse than not displaying the line at all.
func TestTruncationIsVisible(t *testing.T) {
	got := Line("a title that goes well past the column", 12)

	if !strings.HasSuffix(got, string(ellipsis)) {
		t.Errorf("no ellipsis on %q: the truncation is invisible", got)
	}
}

// Counter-proof of the whole file: perfectly normal text comes back out INTACT.
//
// Without this test, a filter removing everything would look correct on the ten hostile families.
func TestLegitimateTextIsUntouched(t *testing.T) {
	for _, s := range []string{
		"The /login endpoint has returned 500 since the deploy",
		"composite foreign key (project_id, team_id)",
		// Diacritics and typographic punctuation on purpose: they are graphic, so the allow list
		// must let them through untouched. A filter that stripped them would pass every hostile
		// family above and still wreck ordinary text.
		"café — naïve — piñata — 42 %",
		"日本語のタイトル",
		"emoji 🚀 in a title",
		"\"double\" and 'single' quotes",
	} {
		if got := Line(s, 0); got != s {
			t.Errorf("Line(%q) = %q — legitimate text was modified", s, got)
		}
	}
}
