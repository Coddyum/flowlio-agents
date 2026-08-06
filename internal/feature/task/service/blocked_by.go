package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                       | Ligne |
// |--------------------|---------------------------------------------------------------|-------|
// | blockedByRef       | One `#blocked-by` line reduced to what an edge needs            | 67    |
// | blockedByLine      | A directive line kept with its position, for the refusal        | 77    |
// | scanBlockedByLines | Collects the directive lines of a body, code fences excluded    | 87    |
// | isFence            | Tells whether a line opens or closes a fenced code block        | 111   |
// | parseBlockedByLine | Reads ONE line, or says precisely why it cannot be read         | 123   |
// | parseBlockedBy     | Reads a whole body, refusing at the first unreadable line       | 165   |
// | previousBlockedBy  | Reads a body already written, keeping only what parses          | 197   |
//
// Fin du sommaire.
// =====================================================================
//
// THE `#blocked-by` LINE, and the reason it is a compiler rather than a convention.
//
// A human writes a dependency where they think about it — in the description of the task that
// waits:
//
//	#blocked-by @CORE-34 until #done
//
// One `@`, one `until`, alone on its line. The line is COMPILED at write time into the same edge
// block_task opens, and nothing about it is decorative: a description that carries the line and no
// edge would be a comment, which is exactly the state this product exists to remove.
//
// Three rules hold this file together, and each of them exists against a defect that was easy to
// write instead:
//
//  1. A line that is DETECTED but unreadable REFUSES the whole write, description included. The
//     alternative — ignoring what cannot be read — makes a typo indistinguishable from a task with
//     no dependency, and the human learns nothing until the day the block they expected never
//     happened.
//
//  2. Detection is deliberately WIDER than the accepted form: anything carrying `#blocked-by` as a
//     word is a directive line, hence refused if it does not match. Recognising only the exact form
//     would let `- #blocked-by @CORE-34` — a bullet, the most natural thing a human writes — pass
//     for prose and vanish without a word.
//
//  3. Fenced code blocks are SKIPPED. A description documenting this very syntax carries the line
//     inside a fence, and a compiler that reads its own documentation would refuse the write that
//     explains it. That is not a corner case: the card describing this feature is written that way.

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// blockedByDirective detects a directive line: the token as a WORD, anywhere on the line. Wider
// than the accepted form on purpose — see rule 2 above.
var blockedByDirective = regexp.MustCompile(`(?i)(^|\s)#blocked-by(\s|$)`)

// blockedByForm is the only accepted shape. The key follows the projects_key_format constraint,
// and `until` is optional: leaving it out reads `done`, the same default as block_task.
var blockedByForm = regexp.MustCompile(`^#blocked-by\s+@([A-Z][A-Z0-9]{1,9})-([0-9]{1,18})(?:\s+until\s+#([a-z_]+))?$`)

// blockedByRef is one directive line reduced to what the edge needs.
//
// The project key does not survive parsing: it has already been checked against the token's own
// project, and an edge cannot cross a repo (D42). Keeping it would only offer a second, unchecked
// way to name a project.
type blockedByRef struct {
	blocker int64
	until   string
}

// blockedByLine is a directive line kept with its 1-based position.
//
// The position is the whole point of the type: a refusal reading "line 12" lets a human find what
// to fix in a description of two hundred lines, where "unreadable dependency line" would send them
// reading the whole thing.
type blockedByLine struct {
	number int
	text   string
}

// scanBlockedByLines collects the directive lines of a body, skipping fenced code blocks.
//
// The fence tracking is intentionally naive — an odd number of fences leaves the tail of the
// document inside a block that never closes. That failure is in the safe direction: lines are
// ignored rather than compiled, so a malformed document opens no edge nobody asked for.
func scanBlockedByLines(body string) []blockedByLine {
	if !strings.Contains(body, "#blocked-by") && !strings.Contains(body, "#BLOCKED-BY") {
		return nil
	}

	var lines []blockedByLine
	fenced := false
	for i, raw := range strings.Split(body, "\n") {
		text := strings.TrimSpace(raw)
		if isFence(text) {
			fenced = !fenced
			continue
		}
		if fenced || !blockedByDirective.MatchString(text) {
			continue
		}
		lines = append(lines, blockedByLine{number: i + 1, text: text})
	}
	return lines
}

// isFence tells whether a trimmed line opens or closes a fenced code block. Both markdown fences
// count: a description is written by a human, who has no reason to know which one the parser
// prefers.
func isFence(text string) bool {
	return strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~")
}

// parseBlockedByLine reads ONE directive line, or says precisely why it cannot be read.
//
// Every refusal names what is wrong AND what was expected: an agent reading "unreadable line" can
// only guess, and guessing is the very thing this file exists to prevent.
//
// selfNumber is the number of the task carrying the description. A line naming it would ask for a
// task blocked by itself — refused here rather than by the CHECK constraint, which would come back
// as a conflict with no reason attached.
func parseBlockedByLine(line blockedByLine, projectKey string, selfNumber int64) (blockedByRef, error) {
	match := blockedByForm.FindStringSubmatch(line.text)
	if match == nil {
		return blockedByRef{}, fmt.Errorf(
			"%w: line %d: %q is not a readable dependency (expected: #blocked-by @%s-12 until #done, alone on its line)",
			ErrInvalidInput, line.number, line.text, projectKey)
	}

	if match[1] != projectKey {
		return blockedByRef{}, fmt.Errorf(
			"%w: line %d: @%s-%s names project %s, and a task can only be blocked by a task of its own repo — a dependency crossing a repo is an issue, not an edge",
			ErrInvalidInput, line.number, match[1], match[2], match[1])
	}

	blocker, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil {
		return blockedByRef{}, fmt.Errorf("%w: line %d: unreadable task number %q",
			ErrInvalidInput, line.number, match[2])
	}
	if blocker == selfNumber {
		return blockedByRef{}, fmt.Errorf("%w: line %d: a task cannot block itself",
			ErrInvalidInput, line.number)
	}

	until := match[3]
	if until == "" {
		until = statusDone
	}
	if !slices.Contains(releaseStatuses, until) {
		return blockedByRef{}, fmt.Errorf(
			"%w: line %d: release condition #%s (expected: %s)",
			ErrInvalidInput, line.number, until, strings.Join(releaseStatuses, ", "))
	}

	return blockedByRef{blocker: blocker, until: until}, nil
}

// parseBlockedBy reads a whole body and refuses at the FIRST unreadable line.
//
// The same blocker named twice is refused as well, and not out of tidiness: the two lines would
// compile into two edges on one pair, which the partial unique index rejects with a conflict that
// says nothing about which line to fix.
func parseBlockedBy(body, projectKey string, selfNumber int64) ([]blockedByRef, error) {
	lines := scanBlockedByLines(body)
	if len(lines) == 0 {
		return nil, nil
	}

	refs := make([]blockedByRef, 0, len(lines))
	seen := make(map[int64]int, len(lines))
	for _, line := range lines {
		ref, err := parseBlockedByLine(line, projectKey, selfNumber)
		if err != nil {
			return nil, err
		}
		if first, ok := seen[ref.blocker]; ok {
			return nil, fmt.Errorf("%w: line %d: @%s-%d is already named on line %d",
				ErrInvalidInput, line.number, projectKey, ref.blocker, first)
		}
		seen[ref.blocker] = line.number
		refs = append(refs, ref)
	}
	return refs, nil
}

// previousBlockedBy reads a body ALREADY WRITTEN, keeping only the lines that parse.
//
// The leniency is not a relaxation of rule 1, it is what keeps that rule from locking a task
// forever. The stored body may predate this compiler, or have been written when the project
// carried another key; refusing on it would make every later edit of that task impossible, and the
// only way out would be an edit — the one being refused.
//
// What matters here is the DIFF with the new body, so an unreadable old line simply opened no
// edge: there is nothing for it to release.
func previousBlockedBy(body, projectKey string, selfNumber int64) []blockedByRef {
	lines := scanBlockedByLines(body)
	refs := make([]blockedByRef, 0, len(lines))
	seen := make(map[int64]bool, len(lines))
	for _, line := range lines {
		ref, err := parseBlockedByLine(line, projectKey, selfNumber)
		if err != nil || seen[ref.blocker] {
			continue
		}
		seen[ref.blocker] = true
		refs = append(refs, ref)
	}
	return refs
}
