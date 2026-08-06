package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                    | Résumé                                                 | Ligne |
// |----------------------------|---------------------------------------------------------|-------|
// | TestParseBlockedByAccepts  | The forms a human may write and what they compile to      | 25    |
// | TestParseBlockedByRefuses  | Every refusal, and what it must say                       | 97    |
// | TestPreviousBlockedByIsLenient | A stored body keeps only what still parses            | 183   |
// | TestDiffRefs               | What a rewrite adds and what it takes away                | 206   |
//
// Fin du sommaire.
// =====================================================================
//
// The parser is tested INSIDE the package, without a database: it decides on its own, and what it
// decides is what refuses a write. A refusal proven here is one that no store double can weaken.

import (
	"errors"
	"strings"
	"testing"
)

// TestParseBlockedByAccepts pins the forms a human may write, and what each compiles to.
func TestParseBlockedByAccepts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []blockedByRef
	}{
		{
			name: "canonical line",
			body: "Brief.\n\n#blocked-by @CORE-56 until #done\n",
			want: []blockedByRef{{blocker: 56, until: "done"}},
		},
		{
			name: "an absent condition reads done, like block_task",
			body: "#blocked-by @CORE-56",
			want: []blockedByRef{{blocker: 56, until: "done"}},
		},
		{
			name: "in_progress is a release condition",
			body: "#blocked-by @CORE-7 until #in_progress",
			want: []blockedByRef{{blocker: 7, until: "in_progress"}},
		},
		{
			name: "two lines, two edges",
			body: "#blocked-by @CORE-7 until #in_progress\ntext\n#blocked-by @CORE-8 until #done",
			want: []blockedByRef{{blocker: 7, until: "in_progress"}, {blocker: 8, until: "done"}},
		},
		{
			name: "surrounding whitespace is not a form",
			body: "   #blocked-by @CORE-56 until #done   ",
			want: []blockedByRef{{blocker: 56, until: "done"}},
		},
		{
			name: "no directive at all",
			body: "A description mentioning nothing in particular.",
			want: nil,
		},
		{
			// The card describing this very feature carries the syntax inside a fence. A compiler that
			// read its own documentation would refuse the write that explains it.
			name: "a fenced block is documentation, not an instruction",
			body: "Example:\n\n```\n#blocked-by @CORE-56 until #done\n```\n\nEnd.",
			want: nil,
		},
		{
			name: "a fence reopens: what follows it is read again",
			body: "```\n#blocked-by @CORE-1\n```\n#blocked-by @CORE-2",
			want: []blockedByRef{{blocker: 2, until: "done"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBlockedBy(tc.body, "CORE", 99)
			if err != nil {
				t.Fatalf("refused a readable body: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%d references read, expected %d (%v)", len(got), len(tc.want), got)
			}
			for i, ref := range got {
				if ref != tc.want[i] {
					t.Errorf("reference %d = %+v, expected %+v", i, ref, tc.want[i])
				}
			}
		})
	}
}

// TestParseBlockedByRefuses covers every refusal, and checks the message names what to fix.
//
// The refusal is the whole point of the feature: a line that cannot be read has to stop the write,
// because ignoring it makes a typo indistinguishable from a task with no dependency.
func TestParseBlockedByRefuses(t *testing.T) {
	cases := []struct {
		name string
		body string
		says string
	}{
		{
			name: "two references on one line",
			body: "#blocked-by @CORE-56 @CORE-57",
			says: "not a readable dependency",
		},
		{
			name: "two conditions",
			body: "#blocked-by @CORE-56 until #done until #in_progress",
			says: "not a readable dependency",
		},
		{
			name: "prose after the line",
			body: "#blocked-by @CORE-56 until #done, and also the rest",
			says: "not a readable dependency",
		},
		{
			// The most natural thing a human writes. Recognised as a directive on purpose: passing it
			// off as prose would make it vanish without a word.
			name: "a bullet is detected and refused",
			body: "- #blocked-by @CORE-56 until #done",
			says: "alone on its line",
		},
		{
			name: "the directive quoted mid-sentence",
			body: "I would write #blocked-by here if I knew the number",
			says: "not a readable dependency",
		},
		{
			name: "another project's key",
			body: "#blocked-by @FRNT-56 until #done",
			says: "names project FRNT",
		},
		{
			name: "a condition that is not progress",
			body: "#blocked-by @CORE-56 until #todo",
			says: "expected: in_progress, done",
		},
		{
			name: "an unknown condition",
			body: "#blocked-by @CORE-56 until #finished",
			says: "expected: in_progress, done",
		},
		{
			name: "the task naming itself",
			body: "#blocked-by @CORE-99 until #done",
			says: "cannot block itself",
		},
		{
			name: "the same blocker twice",
			body: "#blocked-by @CORE-56 until #done\n#blocked-by @CORE-56 until #in_progress",
			says: "already named on line 1",
		},
		{
			name: "a missing number",
			body: "#blocked-by @CORE- until #done",
			says: "not a readable dependency",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBlockedBy(tc.body, "CORE", 99)
			if err == nil {
				t.Fatal("body accepted, expected a refusal")
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("refusal is not an invalid input: %v", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not say %q: %v", tc.says, err)
			}
		})
	}
}

// TestPreviousBlockedByIsLenient proves a STORED body keeps only what still parses.
//
// Without this leniency, a description written before this compiler — or under a project key since
// changed — would refuse every later edit of its task, and the only way out would be the edit being
// refused.
func TestPreviousBlockedByIsLenient(t *testing.T) {
	body := strings.Join([]string{
		"#blocked-by @CORE-7 until #done",
		"#blocked-by @OLD-3 until #done",
		"- #blocked-by @CORE-8",
		"#blocked-by @CORE-9 until #in_progress",
	}, "\n")

	refs := previousBlockedBy(body, "CORE", 99)
	if len(refs) != 2 {
		t.Fatalf("%d references kept, expected 2: %+v", len(refs), refs)
	}
	if refs[0] != (blockedByRef{blocker: 7, until: "done"}) {
		t.Errorf("first reference = %+v", refs[0])
	}
	if refs[1] != (blockedByRef{blocker: 9, until: "in_progress"}) {
		t.Errorf("second reference = %+v", refs[1])
	}
}

// TestDiffRefs pins what a rewrite adds and what it takes away — including the case that is easiest
// to get wrong: a condition changed on the same blocker is one removal AND one addition, never an
// edit, because no query updates until_status after creation.
func TestDiffRefs(t *testing.T) {
	previous := []blockedByRef{{blocker: 7, until: "done"}, {blocker: 8, until: "done"}}
	next := []blockedByRef{{blocker: 8, until: "done"}, {blocker: 9, until: "in_progress"}}

	added := diffRefs(next, previous)
	if len(added) != 1 || added[0].blocker != 9 {
		t.Errorf("additions = %+v, expected only 9", added)
	}
	removed := diffRefs(previous, next)
	if len(removed) != 1 || removed[0].blocker != 7 {
		t.Errorf("removals = %+v, expected only 7", removed)
	}

	changed := diffRefs(
		[]blockedByRef{{blocker: 8, until: "in_progress"}},
		[]blockedByRef{{blocker: 8, until: "done"}},
	)
	if len(changed) != 1 {
		t.Errorf("a changed condition is not a difference: %+v", changed)
	}
}
