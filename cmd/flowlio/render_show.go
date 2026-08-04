package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | renderShow  | Renders one reference: header, body, thread, notes                  | 51    |
// | showHeader  | Composes the first line, which differs between issue and task       | 89    |
// | showContext | Composes the origin, age and deadline line                          | 109   |
//
// Fin du sommaire.
// =====================================================================
//
// SCREEN 2 — THE DETAIL OF ONE ROW OF THE QUEUE.
//
// It answers a single question: "what is being said about CORE-41". It is read after `watch`,
// never instead of it, and it takes one reference — the very string shown in the REF column.
//
// EVERY BODY IS NEUTRALISED THROUGH termtext, AND THAT MATTERS MORE HERE THAN ANYWHERE ELSE. The
// bodies this screen renders were written by ANOTHER repository: a thread message comes from a
// sibling repo, not from the repo being supervised. `termtext.Block` strips the sequences able to
// repaint the terminal — including `\r`, which contains no ESC and therefore slips through any
// filter that only looks for `0x1b`.
//
// The anti-injection framing of `mcp_untrusted.go` is NOT reused here, deliberately: it exists to
// stop an agent from mistaking foreign text for an instruction. The reader of this screen is a
// human, and the human equivalent of framing is ATTRIBUTION — every body is preceded by the repo
// that wrote it, and indented under it.

import (
	"fmt"
	"strings"
	"time"

	overviewservice "github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/termtext"
)

// The two values of `RefDetail.Kind`, as the service writes them (`service/ref_detail.go`). They
// are copied rather than imported: the service does not export them, and exporting them for this
// single use would widen a contract its own documentation says it wants kept narrow.
const (
	refKindIssue = "issue"
	refKindTask  = "task"
)

// renderShow renders the detail of one reference.
//
// now is the reference instant for every age shown. It is passed in rather than read here: that is
// what makes this rendering testable against a literal string.
func renderShow(d overviewservice.RefDetail, now time.Time) string {
	var b strings.Builder

	b.WriteString(showHeader(d) + "\n")
	b.WriteString(termtext.Line(d.Title, 0) + "\n")
	b.WriteString(showContext(d, now) + "\n")

	if body := strings.TrimSpace(termtext.Block(d.Body)); body != "" {
		b.WriteString("\n" + body + "\n")
	}

	if len(d.Messages) > 0 {
		b.WriteString("\n" + sectionTitle("thread", len(d.Messages), d.MessagesTotal) + "\n")
		for _, m := range d.Messages {
			fmt.Fprintf(&b, "  %s · %s ago\n", termtext.Line(m.From, 24), humanAge(m.CreatedAt, now))
			if body := strings.TrimSpace(termtext.Block(m.Body)); body != "" {
				b.WriteString(indent(body, "    ") + "\n")
			}
		}
	}

	if len(d.Notes) > 0 {
		b.WriteString("\n" + sectionTitle("notes", len(d.Notes), d.NotesTotal) + "\n")
		for _, n := range d.Notes {
			fmt.Fprintf(&b, "  %s ago\n", humanAge(n.CreatedAt, now))
			if body := strings.TrimSpace(termtext.Block(n.Body)); body != "" {
				b.WriteString(indent(body, "    ") + "\n")
			}
		}
	}

	return b.String()
}

// showHeader composes the first line: the reference, then what the reference IS.
//
// An unexpected kind is rendered as is rather than treated as a task: a header that surprises is
// better than a header that asserts something false.
func showHeader(d overviewservice.RefDetail) string {
	head := termtext.Line(d.Ref, 0)
	switch d.Kind {
	case refKindIssue:
		return head + " — issue " + termtext.Line(d.State, 24)
	case refKindTask:
		head += " — task " + termtext.Line(d.Status, 24)
		if d.Priority != "" {
			head += " (" + termtext.Line(d.Priority, 24) + ")"
		}
		return head
	default:
		return head + " — " + termtext.Line(d.Kind, 24)
	}
}

// showContext composes the origin and age line.
//
// `from <repo>` only appears on an issue: a task has no foreign sender, and printing an empty
// field would suggest a missing piece of information.
func showContext(d overviewservice.RefDetail, now time.Time) string {
	parts := make([]string, 0, 4)
	if d.From != "" {
		parts = append(parts, "from "+termtext.Line(d.From, 24))
	}
	parts = append(parts, "opened "+humanAge(d.CreatedAt, now)+" ago")
	parts = append(parts, "updated "+humanAge(d.UpdatedAt, now)+" ago")
	if d.Deadline != nil {
		parts = append(parts, deadlineText(*d.Deadline, now))
	}
	return strings.Join(parts, " · ")
}
