package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                            | Ligne |
// |--------------|-------------------------------------------------------------------|-------|
// | humanAge     | Renders an elapsed time as a short label, capped at 7 cells          | 44    |
// | deadlineText | Says whether a deadline is ahead or behind, and by how much          | 63    |
// | pad          | Fills a field up to a display width measured in cells                | 76    |
// | indent       | Shifts a multi-line block, leaving blank lines untouched             | 88    |
// | sectionTitle | Section heading, carrying the total only when it exceeds what shows  | 103   |
//
// Fin du sommaire.
// =====================================================================
//
// Rendering helpers shared by both supervision screens (`watch`, `show`).
//
// EVERYTHING HERE IS PURE. No function in this file — nor in `render_watch.go` or
// `render_show.go` — reads the clock, opens a socket or touches the terminal. The reference
// instant is ALWAYS a parameter. That is what makes both screens testable against literal
// strings, with no terminal and no golden file.
//
// The instant `watch` passes is `TeamState.GeneratedAt`, the SERVER's clock, never the client's:
// the service says so explicitly, and one second of drift between the two would be enough to
// print a negative age on a debt that has just appeared.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/termtext"
)

// humanAge renders the gap between since and now in a short form.
//
// The units are abbreviated — h, d, mo, y — for a reason that is not brevity: a spelled-out unit
// needs a plural rule, and "1 months" in a supervision screen makes the reader doubt the number
// before doubting the wording. The widest output is "59 min", six cells, which is what sets the
// width of the SINCE column.
//
// A negative gap — a server clock trailing a timestamp it wrote itself — is clamped to "<1 min"
// rather than printed as is: "-3s" would cast doubt on the data, when rounding is the real cause.
func humanAge(since, now time.Time) string {
	d := now.Sub(since)
	switch {
	case d < time.Minute:
		return "<1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy", int(d/(365*24*time.Hour)))
	}
}

// deadlineText places a deadline relative to now, in either direction.
func deadlineText(deadline, now time.Time) string {
	if now.After(deadline) {
		return "overdue by " + humanAge(deadline, now)
	}
	return "due in " + humanAge(now, deadline)
}

// pad fills s up to cells display cells.
//
// The width is measured in CELLS, not runes: a CJK title, or one carrying an emoji, takes two
// columns per character, and a `%-20s` would shift the rest of the table. The caller is expected
// to have bounded s to cells-1 through termtext.Line; the gap <= 0 branch still guarantees a
// separator, without which two columns would run into a single unreadable string.
func pad(s string, cells int) string {
	gap := cells - termtext.Cells(s)
	if gap <= 0 {
		return s + " "
	}
	return s + strings.Repeat(" ", gap)
}

// indent shifts every non-empty line of s by prefix.
//
// Blank lines stay blank: prefixing them would produce lines of whitespace that look like content
// in a diff, in a copy-paste or under `cat -A`.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// sectionTitle composes a section heading of the detail screen.
//
// The total is written ONLY when it exceeds what is rendered, exactly as the service only emits it
// in that case: "3 of 3" teaches nothing, "3 of 47" changes how the thread reads.
func sectionTitle(name string, shown, total int) string {
	if total > shown {
		return fmt.Sprintf("%s — %d of %d", name, shown, total)
	}
	return fmt.Sprintf("%s — %d", name, shown)
}
