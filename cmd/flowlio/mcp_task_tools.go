package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                | Ligne |
// |-------------------------|-------------------------------------------------------|-------|
// | mcpServer.listTasks     | Backlog of the current project                          | 40    |
// | mcpServer.createTask    | Opens a task and returns its key                        | 74    |
// | mcpServer.updateTask    | Edits a task, notes it, or archives it                  | 113   |
// | mcpServer.blockTask     | Records that a task waits on another of the same project| 179   |
// | mcpServer.unblockTask   | Lifts one recorded dependency by hand                   | 212   |
// | mcpServer.numberFromKey | Resolves a readable key within the token's project      | 244   |
// | mcpServer.taskPath      | Composes the API path of a task                         | 267   |
// | mcpServer.taskRef       | Composes the readable reference of a number             | 272   |
// | mcpServer.withRefs      | Adds its reference to every task of a listing           | 280   |
//
// Fin du sommaire.
// =====================================================================
//
// Implementation of the task tools. The project is NEVER a parameter: it comes from the token,
// resolved once at startup. No MCP call can therefore name another project's backlog. The issue
// and inbox tools live in mcp_issue_tools.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

const taskAPI = "/api/task"

// listTasks returns the backlog of the current project, each task carrying its readable key.
func (s *mcpServer) listTasks(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Status   string `json:"status"`
		Limit    int    `json:"limit"`
		Archived bool   `json:"archived"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	query := url.Values{}
	if in.Status != "" {
		query.Set("status", in.Status)
	}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Archived {
		query.Set("archived", "true")
	}

	path := taskAPI + "/"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var tasks []taskservice.Task
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &tasks); err != nil {
		return nil, err
	}
	return s.withRefs(tasks), nil
}

// createTask opens a task and returns its key, which is what the agent needs next.
func (s *mcpServer) createTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Deadline string `json:"deadline"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	payload := taskservice.CreateTaskInput{
		Title:    in.Title,
		Body:     in.Body,
		Status:   in.Status,
		Priority: in.Priority,
	}
	deadline, err := parseDeadline(in.Deadline)
	if err != nil {
		return nil, err
	}
	payload.Deadline = deadline

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodPost, taskAPI+"/", payload, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// updateTask edits a task, adds a progress note to it, or archives it.
//
// Archiving and the note are fields of this tool rather than two more tools: they would cost
// their schema in the context of every agent turn, for actions nobody calls without changing a
// status in the same gesture.
//
// The note travels INSIDE the patch and not in a second call: the API writes them in one
// transaction, so "status changed, reason lost" is no longer a reachable state.
func (s *mcpServer) updateTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref           string  `json:"ref"`
		Title         *string `json:"title"`
		Body          *string `json:"body"`
		Status        *string `json:"status"`
		Priority      *string `json:"priority"`
		Deadline      string  `json:"deadline"`
		ClearDeadline bool    `json:"clear_deadline"`
		Note          *string `json:"note"`
		Archive       bool    `json:"archive"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	number, err := s.numberFromKey(in.Ref)
	if err != nil {
		return nil, err
	}

	// ONE SINGLE request, whatever the agent asks for.
	//
	// The status, the note and the archiving travel in the same PATCH, hence in the same
	// transaction. Archiving used to be a second round trip: a failure between the two committed
	// the note without archiving, the agent read `api: internal error` and replayed — which wrote
	// the note a SECOND time. This is not deduplication (docs/DECISION-idempotence.md still
	// stands): it is a non-atomic seam being removed.
	//
	// The deadline is judged on its TRIMMED form, as in parseDeadline. The two bounds diverged for
	// a while — here `!= ""`, there `TrimSpace(...) == ""` — and a deadline made of blanks then
	// crossed this guard only to be ignored right after: the PATCH left with ALL fields nil, the
	// API returned the task unchanged, and the agent read a success believing it had set a
	// deadline. A deadline made of blanks is an absent deadline, everywhere and in the same way.
	if in.Title == nil && in.Body == nil && in.Status == nil && in.Priority == nil &&
		strings.TrimSpace(in.Deadline) == "" && !in.ClearDeadline && in.Note == nil && !in.Archive {
		return nil, errors.New("no change requested")
	}

	payload := taskservice.UpdateTaskInput{
		Title:         in.Title,
		Body:          in.Body,
		Status:        in.Status,
		Priority:      in.Priority,
		ClearDeadline: in.ClearDeadline,
		Note:          in.Note,
		Archive:       in.Archive,
	}
	deadline, err := parseDeadline(in.Deadline)
	if err != nil {
		return nil, err
	}
	payload.Deadline = deadline

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodPatch, s.taskPath(number), payload, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// blockTask records that a task waits on another task of the SAME project.
//
// There is no cross-repo form, and that is not a gap: a dependency that crosses a repo already has
// its object, the issue. The guard holds in the database — both ends of the edge share one
// project_id column — so it cannot be worked around from here either.
func (s *mcpServer) blockTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref   string `json:"ref"`
		On    string `json:"on"`
		Until string `json:"until"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	number, err := s.numberFromKey(in.Ref)
	if err != nil {
		return nil, err
	}
	blocker, err := s.numberFromKey(in.On)
	if err != nil {
		return nil, err
	}

	payload := taskservice.BlockTaskInput{Blocker: blocker, Until: strings.TrimSpace(in.Until)}

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodPost, s.taskPath(number)+"/blockers", payload, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// unblockTask lifts one recorded dependency by hand, without waiting for the blocking task to move.
//
// Replaying it on an already-lifted dependency succeeds and returns the task as it stands: an agent
// that lost its context and replays is not making a mistake, and failing there would break a
// session resume on an action already done.
func (s *mcpServer) unblockTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
		On  string `json:"on"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	number, err := s.numberFromKey(in.Ref)
	if err != nil {
		return nil, err
	}
	blocker, err := s.numberFromKey(in.On)
	if err != nil {
		return nil, err
	}

	path := s.taskPath(number) + "/blockers/" + strconv.FormatInt(blocker, 10)

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// numberFromKey resolves a readable key into a number, WITHIN the token's project.
//
// A key carrying another project's prefix is refused here, with an explicit message. The server
// has no way to serve another project anyway — the refusal tells the agent why, instead of
// leaving it to read a 404 as "this task does not exist".
func (s *mcpServer) numberFromKey(key string) (int64, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return 0, errors.New("missing task key")
	}

	digits := trimmed
	if prefix, suffix, found := strings.Cut(trimmed, "-"); found {
		if !strings.EqualFold(prefix, s.projectKey) {
			return 0, fmt.Errorf("key %s belongs to project %s; this token only gives access to %s",
				trimmed, strings.ToUpper(prefix), s.projectKey)
		}
		digits = suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("invalid task key: %s (expected %s-34)", trimmed, s.projectKey)
	}
	return number, nil
}

// taskPath composes the API path of a task.
func (s *mcpServer) taskPath(number int64) string {
	return taskAPI + "/" + strconv.FormatInt(number, 10)
}

// taskRef composes the readable reference of a number: the only form an agent handles.
func (s *mcpServer) taskRef(number int64) string {
	return fmt.Sprintf("%s-%d", s.projectKey, number)
}

// withRefs adds its reference to every task of a listing.
//
// The reference is carried by the envelope and not by Task: the API works on numbers and knows
// nothing of project keys, composing CORE-34 is this layer's job.
func (s *mcpServer) withRefs(tasks []taskservice.Task) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, writeResult("task", s.taskRef(task.Number), task))
	}
	return out
}
