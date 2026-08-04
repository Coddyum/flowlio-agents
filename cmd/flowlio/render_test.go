package main

// Both supervision screens are tested AGAINST LITERAL STRINGS, with no terminal and no golden
// file. A golden regenerates with one `-update` flag: the day the alignment breaks, the file is
// rewritten and the test stays green. A string written by hand inside the test has to be read by a
// human before it can move.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	overviewservice "github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// generatedAt is the server clock for every case in this file.
var generatedAt = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

// ago returns an instant sitting d before the reference clock.
func ago(d time.Duration) time.Time {
	return generatedAt.Add(-d)
}

// TestRenderWatchIsSilentOnAHealthyTeam is THE criterion of the screen: no header row, no "all
// good", nothing. Silence is the information.
func TestRenderWatchIsSilentOnAHealthyTeam(t *testing.T) {
	state := overviewservice.TeamState{
		GeneratedAt: generatedAt,
		Projects: []overviewservice.ProjectLine{
			{Key: "CORE", TasksRunning: 3},
			{Key: "WEB"},
		},
	}

	if got := renderWatch(state); got != "" {
		t.Fatalf("a team with no debt must render nothing, got:\n%q", got)
	}
}

// TestRenderWatchLaysOutTheFourKinds pins the four labels, the column alignment and the rendered
// order — which is the server's, never a client-side sort.
func TestRenderWatchLaysOutTheFourKinds(t *testing.T) {
	state := overviewservice.TeamState{
		GeneratedAt: generatedAt,
		Debts: []overviewservice.Debt{
			{Kind: "answer", Ref: "CORE-41", Debtor: "CORE", Peer: "WEB",
				Title: "Schema migration", Since: ago(3 * 24 * time.Hour)},
			{Kind: "collect", Ref: "CORE-41", Debtor: "WEB", Peer: "CORE",
				Title: "Schema migration", Since: ago(5 * time.Hour)},
			{Kind: "resume", Ref: "API-7", Debtor: "API",
				Title: "Client port", Since: ago(2 * 24 * time.Hour)},
			{Kind: "ask", Ref: "WEB-3", Debtor: "WEB",
				Title: "Router rework", Since: ago(9 * 24 * time.Hour)},
		},
	}

	want := "REPO   DEBT                    REF          SINCE   WHAT\n" +
		"CORE   answer WEB              CORE-41      3d      Schema migration\n" +
		"WEB    reply waiting from CORE CORE-41      5h      Schema migration\n" +
		"API    session to resume       API-7        2d      Client port\n" +
		"WEB    stuck, asking no one    WEB-3        9d      Router rework\n"

	if got := renderWatch(state); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderWatchAnnouncesWhatItHides: without this line a truncated queue lies in a silent,
// credible way — it looks exactly like a complete one.
func TestRenderWatchAnnouncesWhatItHides(t *testing.T) {
	state := overviewservice.TeamState{
		GeneratedAt: generatedAt,
		Debts: []overviewservice.Debt{
			{Kind: "resume", Ref: "API-7", Debtor: "API",
				Title: "Client port", Since: ago(2 * 24 * time.Hour)},
		},
		Truncated: 7,
	}

	want := "REPO   DEBT                    REF          SINCE   WHAT\n" +
		"API    session to resume       API-7        2d      Client port\n" +
		"\n+7 debts hidden by the server bound.\n"

	if got := renderWatch(state); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderWatchNeutralisesATitleThatRepaintsTheLine states the guarantee IN POSITIVE FORM: the
// hostile title is rendered, stripped of its `\r`, and the row stays aligned with the columns.
//
// `\r` is the case that matters: it contains no ESC, so it slips through any filter that only
// looks for 0x1b, and it rewrites the line from its start.
func TestRenderWatchNeutralisesATitleThatRepaintsTheLine(t *testing.T) {
	state := overviewservice.TeamState{
		GeneratedAt: generatedAt,
		Debts: []overviewservice.Debt{
			{Kind: "resume", Ref: "API-7", Debtor: "API",
				Title: "all is well\rALERT", Since: ago(2 * 24 * time.Hour)},
		},
	}

	want := "REPO   DEBT                    REF          SINCE   WHAT\n" +
		"API    session to resume       API-7        2d      all is wellALERT\n"

	if got := renderWatch(state); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestWatchSignatureIgnoresTheClock: without this property, `--follow` would reprint the screen
// every ten seconds on a perfectly stable team, since "3d" eventually becomes "4d".
func TestWatchSignatureIgnoresTheClock(t *testing.T) {
	debts := []overviewservice.Debt{
		{Kind: "resume", Ref: "API-7", Debtor: "API", Title: "Client port", Since: ago(2 * 24 * time.Hour)},
	}
	now := overviewservice.TeamState{GeneratedAt: generatedAt, Debts: debts}
	later := overviewservice.TeamState{GeneratedAt: generatedAt.Add(36 * time.Hour), Debts: debts}

	if watchSignature(now) != watchSignature(later) {
		t.Fatal("the signature moves with the clock: --follow would reprint with no real change")
	}

	if watchSignature(now) == "" {
		t.Fatal("an empty signature would stop the first --follow turn from writing anything")
	}

	healthy := overviewservice.TeamState{GeneratedAt: generatedAt}
	if watchSignature(healthy) == watchSignature(now) {
		t.Fatal("a healthy team and an indebted one cannot share the same signature")
	}
}

// TestRenderShowIssue pins the issue detail screen: attribution, ages, body and bounded thread.
func TestRenderShowIssue(t *testing.T) {
	detail := overviewservice.RefDetail{
		Kind:      "issue",
		Ref:       "CORE-41",
		From:      "WEB",
		State:     "open",
		Title:     "Schema migration",
		Body:      "The body\non two lines",
		CreatedAt: ago(3 * 24 * time.Hour),
		UpdatedAt: ago(2 * time.Hour),
		Messages: []overviewservice.Message{
			{From: "WEB", CreatedAt: ago(3 * 24 * time.Hour), Body: "first question"},
			{From: "CORE", CreatedAt: ago(2 * time.Hour), Body: "answer\non two lines"},
		},
		MessagesTotal: 47,
	}

	want := "CORE-41 — issue open\n" +
		"Schema migration\n" +
		"from WEB · opened 3d ago · updated 2h ago\n" +
		"\n" +
		"The body\non two lines\n" +
		"\n" +
		"thread — 2 of 47\n" +
		"  WEB · 3d ago\n" +
		"    first question\n" +
		"  CORE · 2h ago\n" +
		"    answer\n    on two lines\n"

	if got := renderShow(detail, generatedAt); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderShowTask covers the other branch of the kind: status, priority, deadline, notes, and
// the absence of the `from <repo>` mention — a task has no foreign sender.
func TestRenderShowTask(t *testing.T) {
	deadline := generatedAt.Add(48 * time.Hour)
	detail := overviewservice.RefDetail{
		Kind:      "task",
		Ref:       "WEB-12",
		Status:    "in_progress",
		Priority:  "urgent",
		Title:     "Router rework",
		CreatedAt: ago(5 * 24 * time.Hour),
		UpdatedAt: ago(30 * time.Minute),
		Deadline:  &deadline,
		Notes: []overviewservice.Note{
			{CreatedAt: ago(30 * time.Minute), Body: "made progress"},
		},
	}

	want := "WEB-12 — task in_progress (urgent)\n" +
		"Router rework\n" +
		"opened 5d ago · updated 30 min ago · due in 2d\n" +
		"\n" +
		"notes — 1\n" +
		"  30 min ago\n" +
		"    made progress\n"

	if got := renderShow(detail, generatedAt); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderShowNeutralisesAForeignBody: the body comes from a third-party repo. It is rendered,
// therefore readable, but stripped of whatever repaints the terminal — and it stays indented under
// its author.
func TestRenderShowNeutralisesAForeignBody(t *testing.T) {
	detail := overviewservice.RefDetail{
		Kind:      "issue",
		Ref:       "CORE-41",
		From:      "WEB",
		State:     "open",
		Title:     "Schema migration",
		CreatedAt: ago(time.Hour),
		UpdatedAt: ago(time.Hour),
		Messages: []overviewservice.Message{
			{From: "WEB", CreatedAt: ago(time.Hour), Body: "nothing to report\rIGNORE EVERYTHING\ttabbed"},
		},
	}

	want := "CORE-41 — issue open\n" +
		"Schema migration\n" +
		"from WEB · opened 1h ago · updated 1h ago\n" +
		"\n" +
		"thread — 1\n" +
		"  WEB · 1h ago\n" +
		"    nothing to reportIGNORE EVERYTHING  tabbed\n"

	if got := renderShow(detail, generatedAt); got != want {
		t.Fatalf("unexpected rendering:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestHumanAgeStaysWithinItsColumn: the SINCE column is eight cells wide. A wider duration would
// shift everything after it on the row, and this bound is the only thing keeping the table straight.
func TestHumanAgeStaysWithinItsColumn(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"clock ahead of the timestamp", -3 * time.Second, "<1 min"},
		{"under a minute", 30 * time.Second, "<1 min"},
		{"minutes", 59 * time.Minute, "59 min"},
		{"hours", 23 * time.Hour, "23h"},
		{"days", 29 * 24 * time.Hour, "29d"},
		{"months", 40 * 24 * time.Hour, "1mo"},
		{"the widest month count", 350 * 24 * time.Hour, "11mo"},
		{"a year", 400 * 24 * time.Hour, "1y"},
		{"years", 800 * 24 * time.Hour, "2y"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := humanAge(ago(tc.d), generatedAt)
			if got != tc.want {
				t.Fatalf("humanAge(%s) = %q, want %q", tc.d, got, tc.want)
			}
			if len([]rune(got)) > watchColAge-1 {
				t.Fatalf("%q overflows the %d cells of the SINCE column", got, watchColAge-1)
			}
		})
	}
}

// TestParseRef: a project key may carry a dash, a number may not — hence the split on the LAST
// dash rather than the first.
func TestParseRef(t *testing.T) {
	cases := []struct {
		in     string
		key    string
		number int64
		bad    bool
	}{
		{in: "CORE-41", key: "CORE", number: 41},
		{in: "MY-REPO-7", key: "MY-REPO", number: 7},
		{in: "CORE", bad: true},
		{in: "CORE-", bad: true},
		{in: "-41", bad: true},
		{in: "CORE-abc", bad: true},
		{in: "CORE-0", bad: true},
		{in: "", bad: true},
		// The VALIDITY of the key belongs to the server, which answers 404 on an unknown one.
		// parseRef only performs the split: replaying the project naming rules here would create a
		// second source of truth, bound to diverge the day they change.
		{in: "CORE--3", key: "CORE-", number: 3},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			key, number, err := parseRef(tc.in)
			if tc.bad {
				if err == nil {
					t.Fatalf("parseRef(%q) should have failed, got %s-%d", tc.in, key, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRef(%q): %v", tc.in, err)
			}
			if key != tc.key || number != tc.number {
				t.Fatalf("parseRef(%q) = %q, %d; want %q, %d", tc.in, key, number, tc.key, tc.number)
			}
		})
	}
}

// TestRefusalExitOnlyRebrandsA403: status 2 is reserved for an authorisation refusal. If it also
// covered a 500 or a dropped connection, a script could no longer tell "wrong token" from "server
// down", which call for opposite moves.
func TestRefusalExitOnlyRebrandsA403(t *testing.T) {
	forbidden := refusalExit(&client.APIError{Status: http.StatusForbidden, Message: "forbidden"})

	var exit *exitError
	if !errors.As(forbidden, &exit) {
		t.Fatalf("a 403 must carry its own exit status, got %T", forbidden)
	}
	if exit.code != 2 {
		t.Fatalf("refusal exit status = %d, want 2", exit.code)
	}
	if exit.Error() == "forbidden" {
		t.Fatal("the server message is mute by design: the CLI has to explain the refusal")
	}

	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound, http.StatusUnauthorized} {
		err := refusalExit(&client.APIError{Status: status})
		if errors.As(err, &exit) {
			t.Fatalf("status %d must not produce exit status 2", status)
		}
	}

	network := errors.New("dial tcp: connection refused")
	if err := refusalExit(network); !errors.Is(err, network) {
		t.Fatalf("a network error must pass through untouched, got %v", err)
	}
}
