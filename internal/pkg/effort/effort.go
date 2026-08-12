package effort

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                              | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | Valid    | Reports whether a string is one of the four tiers                    | 46    |
// | Rank     | The order of a tier as an integer, unknown folding to Standard        | 58    |
// | FromRank | The tier a rank names, clamped into range                            | 70    |
// | Clamp    | Lowers a requested tier to a ceiling, defaulting either when unset    | 85    |
//
// Fin du sommaire.
// =====================================================================
//
// THE EFFORT TIER, the one vocabulary two features share (FLWL-84). An issue's author declares how
// much rigour answering it warrants — never a concrete model, which would couple the repos: the
// author does not know whether claude, codex or opencode answers on the other side. The tier is an
// abstract intent; the receiver's waker maps it to a model for ITS agent, and clamps it to a ceiling
// IT sets. Sender proposes, receiver disposes (docs/MODELE-DE-CONFIANCE.md).
//
// Why the tiers do not sort as strings: `high` < `low` < `max` < `standard` alphabetically, which is
// not their order of rigour. The order lives in Rank, and the probe's SQL mirrors it with a CASE
// (sql/queries/inbox.sql, WakePendingEffort) — the two must stay in step. There is deliberately no
// enum type: a bare string crosses the MCP boundary, the JSON of a probe reply and the text column
// without a conversion at each edge, and Valid is the single gate.

// The four tiers, from the lightest to the most demanding. Low is a throwaway lookup; Max is work
// that warrants the strongest model at full effort.
const (
	Low      = "low"
	Standard = "standard"
	High     = "high"
	Max      = "max"
)

// Default is the tier an issue carries when its author declared none: the middle-ground standard, so
// an unspecified issue neither burns the strongest model nor is starved of one.
const Default = Standard

// order lists the tiers by increasing rigour. Its index IS the rank, so Rank and FromRank read from
// the one place and can never disagree.
var order = []string{Low, Standard, High, Max}

// Valid reports whether s is one of the four tiers. An empty string is NOT valid: "unspecified" is a
// distinct case the callers fold to Default themselves, so Valid can stay a plain membership test.
func Valid(s string) bool {
	for _, t := range order {
		if t == s {
			return true
		}
	}
	return false
}

// Rank yields the order of a tier as an integer — Low 0, Standard 1, High 2, Max 3 — so tiers can be
// compared and a maximum taken. Anything unknown, the empty string included, folds to Standard's
// rank: a rank is always answerable, and the neutral default is the safe fold.
func Rank(s string) int {
	for i, t := range order {
		if t == s {
			return i
		}
	}
	return 1 // Standard
}

// FromRank yields the tier a rank names, clamping an out-of-range rank into [Low, Max] rather than
// panicking. It is the inverse of Rank, and the seam the probe crosses: its SQL returns a rank
// integer (a string max would not order right), and this turns it back into a tier name.
func FromRank(r int) string {
	if r < 0 {
		r = 0
	}
	if r >= len(order) {
		r = len(order) - 1
	}
	return order[r]
}

// Clamp lowers a requested tier to a ceiling, and is the whole of "receiver disposes". want is what
// the issue's author asked for; ceiling is what the receiver's daemon permits. An invalid or empty
// want folds to Default — a wake still picks a model. An invalid or empty ceiling means "no ceiling
// set", so the want stands. Otherwise the lower of the two wins: a hostile author cannot lift a wake
// above the receiver's policy, which is what stops the tier being a cost-amplification lever.
func Clamp(want, ceiling string) string {
	w := want
	if !Valid(w) {
		w = Default
	}
	if !Valid(ceiling) {
		return w
	}
	if Rank(w) > Rank(ceiling) {
		return ceiling
	}
	return w
}
