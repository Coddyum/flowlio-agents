package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                  | Ligne |
// |------------------------|---------------------------------------------------------|-------|
// | mcpServer.createIssue  | Asks a sibling project a question                         | 41    |
// | mcpServer.listIssues   | The questions exchanged with the sibling projects          | 62    |
// | mcpServer.answerIssue  | Adds a message to an issue thread, and closes it          | 108   |
// | mcpServer.checkInbox   | What awaits the agent, with no parameter at all           | 144   |
// | mcpServer.issuePath    | Composes the API path of an issue                         | 158   |
// | splitRef               | Splits CORE-34 into a project key and a number            | 168   |
//
// Fin du sommaire.
// =====================================================================
//
// Issues are the heart of the product: two repositories talking to each other without the human
// carrying the messages. A reference ALWAYS carries the key of the addressee project, which owns
// the issue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	inboxservice "github.com/Coddyum/flowlio-agents/internal/feature/inbox/service"
	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

const (
	issueAPI = "/api/issue"
	inboxAPI = "/api/inbox"
)

// createIssue asks a question to a sibling project of the team.
func (s *mcpServer) createIssue(ctx context.Context, args json.RawMessage) (any, error) {
	var in issueservice.CreateIssueInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	in.ToProject = strings.ToUpper(strings.TrimSpace(in.ToProject))
	if in.ToProject == s.projectKey {
		return nil, fmt.Errorf(
			"a question to one's own project (%s) is a task — use create_task",
			s.projectKey)
	}

	var issue issueservice.Issue
	if err := s.api.Do(ctx, http.MethodPost, issueAPI+"/", in, &issue); err != nil {
		return nil, err
	}
	return writeResult("issue", issue.Ref, issue), nil
}

// listIssues returns the questions exchanged with the sibling projects.
func (s *mcpServer) listIssues(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Role   string `json:"role"`
		State  string `json:"state"`
		Limit  int    `json:"limit"`
		Closed bool   `json:"closed"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	query := url.Values{}
	if in.Role != "" {
		query.Set("role", in.Role)
	}
	if in.State != "" {
		query.Set("state", in.State)
	}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Closed {
		query.Set("closed", "true")
	}

	path := issueAPI + "/"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var issues []issueservice.Issue
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &issues); err != nil {
		return nil, err
	}

	// The title of an incoming issue is written by the peer, and 200 characters are enough to
	// hold an instruction. A listing carries no reading notice: one line per issue, and the full
	// rule is already in the session instructions.
	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return f.markIssues(issues), nil
}

// answerIssue adds a message to an issue thread, and closes it when asked.
func (s *mcpServer) answerIssue(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref   string `json:"ref"`
		Body  string `json:"body"`
		Close bool   `json:"close"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	projectKey, number, err := splitRef(in.Ref, s.projectKey)
	if err != nil {
		return nil, err
	}

	payload := issueservice.AnswerInput{Body: in.Body, Close: in.Close}
	var issue issueservice.Issue
	path := s.issuePath(projectKey, number) + "/answer"
	if err := s.api.Do(ctx, http.MethodPost, path, payload, &issue); err != nil {
		return nil, err
	}

	// Answering an INCOMING issue returns its title, written by the peer: it is marked as
	// everywhere else. The write envelope stays {ref, object} — no reading notice here, the agent
	// has just written, it discovers nothing.
	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return writeResult("issue", issue.Ref, f.markIssue(issue)), nil
}

// checkInbox returns what awaits the agent. No parameter: everything comes from the token.
//
// This is the first call of a session, hence the first place where text written by another
// repository enters the agent's context. Everything the peer wrote is marked there.
func (s *mcpServer) checkInbox(ctx context.Context, _ json.RawMessage) (any, error) {
	var inbox inboxservice.Inbox
	if err := s.api.Do(ctx, http.MethodGet, inboxAPI+"/", nil, &inbox); err != nil {
		return nil, err
	}

	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return inboxResult{Reading: f.notice(), Inbox: f.markInbox(inbox)}, nil
}

// issuePath composes the API path of an issue.
func (s *mcpServer) issuePath(projectKey string, number int64) string {
	return issueAPI + "/" + url.PathEscape(projectKey) + "/" + strconv.FormatInt(number, 10)
}

// splitRef splits CORE-34 into a project key and a number.
//
// Unlike tasks, the key may name a sibling project: an issue belongs to its addressee, which is
// not always the caller. A bare number names the current project. Access control is NOT done here
// — it lives in the query: a reference pointing at a conversation the caller takes no part in
// simply comes back as "not found".
func splitRef(ref, defaultKey string) (string, int64, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", 0, errors.New("missing reference")
	}

	projectKey, digits := defaultKey, trimmed
	if prefix, suffix, found := strings.Cut(trimmed, "-"); found {
		// An empty prefix ("-34") would produce an API path with an empty segment, hence a route
		// that matches nothing: refusing it here gives a useful message.
		if prefix == "" {
			return "", 0, fmt.Errorf("invalid reference: %s (expected %s-34)", trimmed, defaultKey)
		}
		projectKey, digits = strings.ToUpper(prefix), suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return "", 0, fmt.Errorf("invalid reference: %s (expected %s-34)", trimmed, defaultKey)
	}
	return projectKey, number, nil
}
