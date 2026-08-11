package feature_test

// THE LOOP CLOSES WITHOUT A HUMAN — proved end to end (DESIGN-WAKE §8, §11 acceptance).
//
// The pieces are tested apart elsewhere; this drives them together through the REAL issue service.
// A repo asks a question, and the recipient's registered waker is POSTed to on 127.0.0.1 — a dead
// session there would be relaunched. The recipient answers, and the author's waker is POSTed to in
// turn. No polling, no human: an event on the journal becomes a wake on the other machine.
//
// The wakers here are httptest servers standing in for `flowlio waker` — the transport is what is
// under test, not the launch. Each checks it was called WITH ITS SECRET, which is the §9 guard the
// engine's push carries.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/database"
	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	issuestore "github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	workspacestore "github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// fakeWaker is an httptest server standing in for a repo's local waker. It reports the Authorization
// header of each wake it receives, so a test can check the secret rode along.
func fakeWaker(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func awaitWake(t *testing.T, wakes <-chan string, wantAuth, who string) {
	t.Helper()
	select {
	case auth := <-wakes:
		if auth != wantAuth {
			t.Errorf("%s woken with Authorization %q, want %q", who, auth, wantAuth)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s was never woken — the loop did not close", who)
	}
}

func TestWakeLoopClosesWithoutAHuman(t *testing.T) {
	db := openTrustDB(t)
	ctx := context.Background()
	ws := workspacestore.New(database.New(db))
	team := newTrustTeam(t, ws, db)

	// WEB first (no edge yet), then CORE — the second repo links both ways, so WEB may question CORE
	// and CORE may answer.
	if _, err := ws.CreateProject(ctx, team.ID, "WEB", "web"); err != nil {
		t.Fatalf("CreateProject WEB: %v", err)
	}
	if _, err := ws.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	web, err := ws.ProjectByKey(ctx, team.ID, "WEB")
	if err != nil {
		t.Fatalf("ProjectByKey WEB: %v", err)
	}
	core, err := ws.ProjectByKey(ctx, team.ID, "CORE")
	if err != nil {
		t.Fatalf("ProjectByKey CORE: %v", err)
	}

	// One cache shared by the issue service and the wake registry, as ModuleConfig shares it.
	c := cache.NewMemory(time.Hour, time.Hour)
	issues := issueservice.New(issuestore.New(database.New(db), db, c), c)

	// CORE's waker is registered: a question for CORE must reach it.
	coreSrv, coreWakes := fakeWaker(t)
	wakepush.Register(c, team.ID, core.ID, wakepush.Registration{Callback: coreSrv.URL, Secret: "core-secret"}, time.Hour)

	issue, err := issues.CreateIssue(ctx, issueservice.CreateIssueInput{
		TeamID:          team.ID,
		AuthorProjectID: web.ID,
		ToProject:       "CORE",
		Title:           "why does the build fail?",
		Body:            "the pipeline is red since your last change",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	awaitWake(t, coreWakes, "Bearer core-secret", "CORE (the recipient)")

	// Now WEB's waker is registered, and CORE answers: the author must be woken in turn.
	webSrv, webWakes := fakeWaker(t)
	wakepush.Register(c, team.ID, web.ID, wakepush.Registration{Callback: webSrv.URL, Secret: "web-secret"}, time.Hour)

	number := parseRefNumber(t, issue.Ref)
	if _, err := issues.Answer(ctx, issueservice.AnswerInput{
		Ref: issueservice.Ref{
			TeamID:          team.ID,
			CallerProjectID: core.ID,
			ProjectKey:      "CORE",
			Number:          number,
		},
		Body: "fixed on our side, pull again",
	}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	awaitWake(t, webWakes, "Bearer web-secret", "WEB (the author)")
}

// parseRefNumber pulls the number out of a CORE-34 reference.
func parseRefNumber(t *testing.T, ref string) int64 {
	t.Helper()
	parts := strings.SplitN(ref, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("unreadable ref %q", ref)
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("unreadable ref number in %q: %v", ref, err)
	}
	return n
}
