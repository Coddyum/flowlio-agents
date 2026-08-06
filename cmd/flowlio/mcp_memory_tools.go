package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                  | Ligne |
// |-----------------------|---------------------------------------------------------|-------|
// | mcpServer.remember    | Writes one entry, and retires what it replaces            | 64    |
// | mcpServer.recall      | Lists or searches the project's memory                    | 96    |
// | mcpServer.memoryIndex | Reads the index injected into the handshake               | 139   |
// | closesTask            | Says whether a patch ends a task                          | 166   |
//
// Fin du sommaire.
// =====================================================================
//
// TWO TOOLS, AND THAT IS A BUDGET RATHER THAN A PREFERENCE. Every tool is re-injected into the
// agent's context on EVERY turn: `remember` and `recall` is the smallest pair that covers writing,
// reading, searching and retiring.
//
// WHAT IS DELIBERATELY NOT A TOOL:
//   - the index. It is injected into `initialize.instructions`, once per session, before the
//     agent's first message. A tool would make reading the memory OPTIONAL, and the whole design
//     rests on it not being.
//   - forgetting. An entry is never edited and never deleted, it is superseded — through the
//     `supersedes` field of `remember`. A delete would remove the one thing this feature has over
//     a markdown file: "why was it like that" stays answerable next to "why is it like this".
//   - a per-kind tool. Three tools where a `kind` enum does the job would cost three schemas on
//     every turn to express what one property already says.
//
// HOW WRITING GETS FORCED, and it is not by asking in a description. The question is hooked onto
// `update_task(status=done)` — a gesture the agent makes anyway — and it is the ANSWER TO THAT
// CALL that carries it. See closingQuestion, and mcp_task_tools.go where it is attached.
//
// THE QUESTION IS MANDATORY, THE ENTRY IS NOT. A hard lock produces entries written to pass the
// lock. The measurement is in the card: in the reference system, the register demanding a write
// for its own sake holds nothing but its template after months, while the ones hooked onto a
// moment that already exists are full. Asking is what works; requiring is what empties.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	memoryservice "github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
)

// memoryAPI is the prefix of the memory module's routes.
const memoryAPI = "/api/memory"

// memoryKinds mirrors the server's vocabulary exactly. Restating it here puts it in the schema,
// hence in the agent's context: it has neither to guess nor to fail once in order to learn.
var memoryKinds = []string{
	memoryservice.KindDecision,
	memoryservice.KindLearning,
	memoryservice.KindState,
}

// remember writes one entry, and retires what it replaces in the same transaction.
func (s *mcpServer) remember(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Slug       string   `json:"slug"`
		Kind       string   `json:"kind"`
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		Supersedes []string `json:"supersedes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}
	if strings.TrimSpace(in.Slug) == "" {
		return nil, errors.New("an entry needs a slug: it is what other entries and commits cite it by")
	}

	payload := memoryservice.RememberInput{
		Slug:       strings.TrimSpace(in.Slug),
		Kind:       in.Kind,
		Title:      in.Title,
		Body:       in.Body,
		Supersedes: in.Supersedes,
	}

	var entry memoryservice.Entry
	if err := s.api.Do(ctx, http.MethodPost, memoryAPI+"/", payload, &entry); err != nil {
		return nil, err
	}
	return map[string]any{"slug": entry.Slug, "memory": entry}, nil
}

// recall lists or searches. The presence of `query` decides which, so an agent that does not know
// whether it is looking for something or browsing does not have to choose a tool.
func (s *mcpServer) recall(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Query   string `json:"query"`
		Kind    string `json:"kind"`
		History bool   `json:"history"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	q := url.Values{}
	if v := strings.TrimSpace(in.Query); v != "" {
		q.Set("q", v)
	}
	if in.Kind != "" {
		q.Set("kind", in.Kind)
	}
	if in.History {
		q.Set("history", "true")
	}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}

	path := memoryAPI + "/"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var recalled memoryservice.Recalled
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &recalled); err != nil {
		return nil, err
	}
	return recalled, nil
}

// memoryIndex reads the titles in force, for the handshake instructions.
//
// BEST EFFORT, AND THAT IS DELIBERATE: a failure here returns an empty index and no error. The
// index is a convenience injected into the instructions; an unreachable API or a project with no
// memory must not stop a session from starting. The agent finds out soon enough through its first
// tool call, with a real message.
func (s *mcpServer) memoryIndex(ctx context.Context) []memoryservice.IndexLine {
	var lines []memoryservice.IndexLine
	if err := s.api.Do(ctx, http.MethodGet, memoryAPI+"/index", nil, &lines); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio: memory index unavailable: %v\n", err)
		return nil
	}
	return lines
}

// closingQuestion is what a closed task is answered with. It is the ENTIRE write-side mechanism of
// this feature, and it is one sentence attached to a call the agent was making anyway.
//
// It says "nothing to remember is a valid answer" in so many words, because the alternative is an
// agent inventing an entry to satisfy what reads like a requirement — which is exactly how a
// register fills up with noise and stops being read.
const closingQuestion = "This task is closed. What had to be remembered from it — a decision and " +
	"what it replaces, or something that will bite again? Write it with remember. Nothing worth " +
	"keeping is a valid answer: say nothing and move on."

// closesTask says whether a patch ends a task — the moment the memory question is attached to.
//
// Archiving counts as much as moving to done: an archived task is over too, and it is the shape an
// agent uses to close something it will not finish. Missing it would silence the question on half
// the closes.
//
// It lives HERE and not in mcp_task_tools.go because it is part of the memory mechanism, not of
// the task one: what it decides is when to ask, and the asking belongs to this file.
func closesTask(status *string, archive bool) bool {
	if archive {
		return true
	}
	return status != nil && *status == "done"
}
