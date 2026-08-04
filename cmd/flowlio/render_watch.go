package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                          | Ligne |
// |----------------|-----------------------------------------------------------------|-------|
// | renderWatch    | Renders the debt queue, or the empty string on a healthy team     | 57    |
// | debtLabel      | Names a debt by the ACT owed, not by its protocol code            | 92    |
// | watchSignature | Digests the state so --follow only reprints on a real change      | 115   |
//
// Fin du sommaire.
// =====================================================================
//
// SCREEN 1 — A DEBT QUEUE, NOT A DASHBOARD.
//
// On a healthy team this screen is EMPTY, and that is the best information the product knows how
// to produce: silence says "nothing is rotting", which no green dashboard ever really says. A
// courtesy line — an "all good", one row per repo, a counter at zero — would delete that
// information by drowning it.
//
// This is why `TeamState.Projects` is NOT rendered here, even though the response carries it.
// Those counters are a state, not a debt: `tasks_running: 3` is true on a perfectly healthy team,
// and the screen would stop being empty as soon as an agent works. Repo pulse belongs to the web
// UI, which has the room to show it without drowning anything.
//
// THE ORDER COMES FROM THE SERVER AND IS NOT REPLAYED. The service sorts by `Since` ascending —
// the opposite of everything else in this repo — so that the FIRST row is, by construction, the
// worst thing in the system. Re-sorting here would break that guarantee without turning a single
// service test red.

import (
	"fmt"
	"strings"

	overviewservice "github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/termtext"
)

// Column widths, in display cells. Worst case: 102 columns, title included.
//
// REPO and REF are not redundant: they coincide on `resume` and `ask`, but diverge on `collect`,
// where the issue is `CORE-41` while WEB is the one who must go read the answer. Merging the two
// columns would make that row — the only one a supervisor is likely to misattribute — look exactly
// like the others.
const (
	watchColRepo  = 7
	watchColDebt  = 24
	watchColRef   = 13
	watchColAge   = 8
	watchColTitle = 50
)

// renderWatch renders a team's debt queue.
//
// Its only argument is the state returned by the server: each row's age is computed from
// `GeneratedAt`, never from the local clock.
func renderWatch(state overviewservice.TeamState) string {
	if len(state.Debts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(pad("REPO", watchColRepo))
	b.WriteString(pad("DEBT", watchColDebt))
	b.WriteString(pad("REF", watchColRef))
	b.WriteString(pad("SINCE", watchColAge))
	b.WriteString("WHAT\n")

	for _, d := range state.Debts {
		b.WriteString(pad(termtext.Line(d.Debtor, watchColRepo-1), watchColRepo))
		b.WriteString(pad(termtext.Line(debtLabel(d.Kind, d.Peer), watchColDebt-1), watchColDebt))
		b.WriteString(pad(termtext.Line(d.Ref, watchColRef-1), watchColRef))
		b.WriteString(pad(humanAge(d.Since, state.GeneratedAt), watchColAge))
		b.WriteString(termtext.Line(d.Title, watchColTitle))
		b.WriteString("\n")
	}

	// Without this line a truncated queue lies in a silent, credible way: it looks exactly like a
	// complete one.
	if state.Truncated > 0 {
		fmt.Fprintf(&b, "\n+%d debts hidden by the server bound.\n", state.Truncated)
	}

	return b.String()
}

// debtLabel names the debt by the ACT owed, not by the protocol kind.
//
// "answer" teaches nothing to someone discovering the screen; "answer WEB" says both what to do
// and to whom. An unknown kind is rendered as is rather than hidden: the day the server adds a
// fifth one, the row stays visible and readable instead of vanishing from the queue.
func debtLabel(kind, peer string) string {
	switch kind {
	case overviewservice.KindAnswer:
		return "answer " + peer
	case overviewservice.KindCollect:
		return "reply waiting from " + peer
	case overviewservice.KindResume:
		return "session to resume"
	case overviewservice.KindAsk:
		return "stuck, asking no one"
	default:
		return kind
	}
}

// watchSignature digests the state into a string comparable from one loop turn to the next.
//
// IT CARRIES NO AGE, AND THAT IS THE WHOLE POINT. Comparing successive renderings would not work:
// "3d" becomes "4d" without any debt having moved, and `--follow` would reprint the screen every
// ten seconds on a perfectly stable team.
//
// It is never empty, not even without debts: a healthy state must be able to DIFFER from the
// initial value, otherwise `--follow` would start mute and be indistinguishable from a dead client.
func watchSignature(state overviewservice.TeamState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n=%d;t=%d", len(state.Debts), state.Truncated)
	for _, d := range state.Debts {
		fmt.Fprintf(&b, ";%s|%s|%d", d.Kind, d.Ref, d.Since.UnixNano())
	}
	return b.String()
}
